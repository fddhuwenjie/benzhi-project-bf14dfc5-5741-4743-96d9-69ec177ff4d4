package audit_cache_stale_test

import (
	"fmt"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/store"
	"testing"
	"time"
)

func TestAuditCacheInvalidatedAfterCommit(t *testing.T) {
	repo, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	incident := &domain.PreservationIncident{
		ID:       "audit-cache-incident",
		Status:   domain.StatusPending,
		Revision: 1,
		Timeline: auditTimeline("audit-cache-incident", 128, now),
	}
	first := domain.RequestRecord{RequestID: "audit-cache-create", Operation: "create", IncidentID: incident.ID, Digest: "create", SuccessRevision: 1, Result: incident}
	if err = repo.Commit(incident, 0, first); err != nil {
		t.Fatal(err)
	}
	if events, auditErr := repo.AuditEvents(incident.ID); auditErr != nil || len(events) != 128 {
		t.Fatalf("首次审计读取失败: events=%d err=%v", len(events), auditErr)
	}

	updated, err := repo.Get(incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated.Revision = 2
	updated.Timeline = append(updated.Timeline, domain.IncidentEvent{
		ID: "audit-cache-incident-129", IncidentID: incident.ID, Sequence: 129,
		EventType: "分派", Actor: "负责人", OccurredAt: now.Add(time.Minute), RevisionBefore: 1, RevisionAfter: 2,
	})
	second := domain.RequestRecord{RequestID: "audit-cache-assign", Operation: "assignment", IncidentID: incident.ID, Digest: "assign", SuccessRevision: 2, Result: updated}
	if err = repo.Commit(updated, 1, second); err != nil {
		t.Fatal(err)
	}
	events, err := repo.AuditEvents(incident.ID)
	if err != nil {
		t.Fatalf("成功提交后审计缓存未随日志版本失效: %v", err)
	}
	if len(events) != 129 {
		t.Fatalf("成功提交后审计事件数错误: got %d want 129", len(events))
	}
}

func auditTimeline(incidentID string, count int, occurredAt time.Time) []domain.IncidentEvent {
	events := make([]domain.IncidentEvent, count)
	for index := range events {
		sequence := index + 1
		events[index] = domain.IncidentEvent{
			ID: incidentID + "-" + fmt.Sprint(sequence), IncidentID: incidentID, Sequence: sequence,
			EventType: "措施过程记录", Actor: "执行人", OccurredAt: occurredAt.Add(time.Duration(index) * time.Second),
		}
	}
	return events
}
