package deadline_history_reuse_test

import (
	"fmt"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/store"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestSecondLargeDeadlineRequestPreservesApprovedHistory(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	repo, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	items := make([]domain.MitigationItem, 32)
	affected := make([]string, len(items))
	for n := range items {
		id := fmt.Sprintf("measure-%02d", n+1)
		items[n] = domain.MitigationItem{ID: id, Description: "处置措施", Status: "待执行"}
		affected[n] = id
	}
	incident := &domain.PreservationIncident{
		ID:       "deadline-history-incident",
		Status:   domain.StatusMitigating,
		Revision: 1,
		Assignee: "执行人",
		DueAt:    now.Add(time.Hour),
		Plan:     &domain.MitigationPlan{Owner: "执行人", DueAt: now.Add(time.Hour), Items: items},
	}
	if err = repo.Save(incident, 0); err != nil {
		t.Fatal(err)
	}

	svc := &workflow.Service{Repo: repo, Now: func() time.Time { return now }}
	first, err := svc.RequestDeadlineChange(incident.ID, 1, now.Add(2*time.Hour), "首批措施等待设备", affected, "执行人", "deadline-request-first")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := svc.DecideDeadlineChange(incident.ID, first.Revision, true, "负责人", "批准首次延期", "deadline-decision-first")
	if err != nil {
		t.Fatal(err)
	}
	if len(approved.DeadlineChangeHistory) != 1 || approved.DeadlineChangeHistory[0].Status != "已批准" {
		t.Fatalf("首次审批未形成历史记录: %#v", approved.DeadlineChangeHistory)
	}

	_, err = svc.RequestDeadlineChange(incident.ID, approved.Revision, now.Add(3*time.Hour), "第二批措施等待材料", affected, "执行人", "deadline-request-second")
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := restarted.Get(incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := stored.DeadlineChangeHistory[0]
	if got.ID != "deadline-history-incident-deadline-1" || got.Status != "已批准" || got.Reason != "首批措施等待设备" || got.DecidedAt == nil {
		t.Fatalf("第二次待审批申请覆盖了已批准历史: %#v", got)
	}
}
