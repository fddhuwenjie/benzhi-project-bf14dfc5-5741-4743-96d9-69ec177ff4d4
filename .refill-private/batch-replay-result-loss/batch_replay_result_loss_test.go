package batch_replay_result_loss_test

import (
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/store"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestBatchReplayRestoresResultsAfterRestart(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	repo, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	entries := make([]workflow.BatchAssignmentEntry, 0, 2)
	for _, id := range []string{"restart-batch-a", "restart-batch-b"} {
		incident, createErr := domain.NewIncident(
			id,
			"库房甲",
			"纸本文物",
			"低",
			now.Add(-10*time.Minute),
			[]domain.EnvironmentalReading{{
				ID: id + "-humidity", Phase: domain.PhaseAbnormal, Metric: "湿度",
				Value: 70, Unit: "%RH", MeasuredAt: now.Add(-10 * time.Minute),
			}},
			domain.RiskMedium,
			[]string{"湿度超标"},
			8*time.Hour,
		)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if saveErr := repo.Save(incident, 0); saveErr != nil {
			t.Fatal(saveErr)
		}
		entries = append(entries, workflow.BatchAssignmentEntry{IncidentID: id, ExpectedRevision: incident.Revision})
	}

	command := workflow.BatchAssignmentCommand{
		Entries: entries, Assignee: "执行人甲", DueAt: now.Add(time.Hour), Summary: "标准控湿",
		Items: []domain.MitigationItem{{ID: "humidity-control", Description: "启动除湿", CoveredMetrics: []string{"湿度"}}},
		Actor: "负责人", RequestID: "restart-batch-request",
	}
	service := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	first, err := service.AssignBatch(command)
	if err != nil {
		t.Fatalf("首次批量分派失败: %v", err)
	}
	if len(first.Incidents) != len(entries) || len(first.Results) != len(entries) {
		t.Fatalf("首次批量分派结果不完整: incidents=%d results=%d", len(first.Incidents), len(first.Results))
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &workflow.Service{Repo: reopened, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	replayed, err := restarted.AssignBatch(command)
	if err != nil {
		t.Fatalf("重启后的幂等重放失败: %v", err)
	}
	if len(replayed.Incidents) != len(entries) || len(replayed.Results) != len(entries) {
		t.Fatalf("重启后的批量幂等结果丢失: incidents=%d results=%d, want=%d", len(replayed.Incidents), len(replayed.Results), len(entries))
	}
	for n, result := range replayed.Results {
		if !result.Valid || result.IncidentID != entries[n].IncidentID || result.Revision != 2 || result.Status != domain.StatusMitigating {
			t.Fatalf("重放结果 %d 不完整: %#v", n, result)
		}
	}
}
