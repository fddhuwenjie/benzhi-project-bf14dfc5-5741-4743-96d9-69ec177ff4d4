package large_list_retest_alias_test

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/store"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestLargeListDoesNotPersistDerivedRetestState(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	for n := 0; n < 64; n++ {
		id := fmt.Sprintf("incident-%03d", n)
		incident := &domain.PreservationIncident{
			ID:          id,
			AreaID:      "库房-A",
			Status:      domain.StatusPending,
			RiskLevel:   domain.RiskMedium,
			Revision:    1,
			ObservedAt:  now.Add(-time.Hour),
			ResponseDue: 4 * time.Hour,
			CreatedAt:   now.Add(-time.Hour),
			UpdatedAt:   now.Add(-time.Hour),
		}
		if n == 0 {
			incident.Status = domain.StatusMitigating
			incident.DueAt = now.Add(4 * time.Hour)
			incident.RetestCheckpoints = []domain.RetestCheckpoint{{
				ID:               "retest-1",
				Metric:           "温度",
				PlannedAt:        now.Add(-2 * time.Hour),
				AllowedDeviation: 30 * time.Minute,
				Status:           "待复测",
			}}
		}
		if err := st.MemoryRepo.Commit(incident, 0, domain.RequestRecord{}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	svc := workflow.Service{Repo: st, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	result := svc.List(domain.IncidentFilter{})
	if len(result.Incidents) != 64 {
		t.Fatalf("list returned %d incidents", len(result.Incidents))
	}

	unrelated, err := st.Get("incident-001")
	if err != nil {
		t.Fatalf("get unrelated incident: %v", err)
	}
	unrelated.Revision++
	unrelated.UpdatedAt = now
	if err := st.Commit(unrelated, 1, domain.RequestRecord{}); err != nil {
		t.Fatalf("persist unrelated incident: %v", err)
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	target, err := reopened.Get("incident-000")
	if err != nil {
		t.Fatalf("get target after restart: %v", err)
	}
	if got := target.RetestCheckpoints[0].Status; got != "待复测" {
		t.Fatalf("list projection leaked derived retest status into persisted aggregate: got %q", got)
	}
}
