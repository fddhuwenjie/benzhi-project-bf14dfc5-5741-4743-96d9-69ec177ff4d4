package restart_log_recovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"museum-preservation/internal/domain"
	"museum-preservation/internal/store"
)

func TestRestartReplaysCommittedEventLogWhenSnapshotMissing(t *testing.T) {
	dir := t.TempDir()
	repo, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	observed := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	reading := domain.EnvironmentalReading{
		ID: "reading-1", IncidentID: "incident-1", Phase: domain.PhaseAbnormal,
		Metric: "温度", Value: 31, Unit: "℃", MeasuredAt: observed,
	}
	incident, err := domain.NewIncident("incident-1", "库房-A", "青铜器柜", "中", observed, []domain.EnvironmentalReading{reading}, domain.RiskHigh, []string{"温度超阈值"}, 24*time.Hour)
	if err != nil {
		t.Fatalf("new incident: %v", err)
	}
	incident.SetRegistrationDetails(nil)
	if err := repo.Save(incident, 0); err != nil {
		t.Fatalf("commit incident: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err != nil {
		t.Fatalf("event log was not persisted: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "snapshot.json")); err != nil {
		t.Fatalf("simulate lost snapshot: %v", err)
	}

	restarted, err := store.Open(dir)
	if err != nil {
		t.Fatalf("restart should recover from event log: %v", err)
	}
	recovered, err := restarted.Get("incident-1")
	if err != nil {
		t.Fatalf("expected committed incident after restart, got %v", err)
	}
	if recovered.Revision != incident.Revision || len(recovered.Timeline) != len(incident.Timeline) {
		t.Fatalf("recovered state mismatch: revision=%d timeline=%d", recovered.Revision, len(recovered.Timeline))
	}
}
