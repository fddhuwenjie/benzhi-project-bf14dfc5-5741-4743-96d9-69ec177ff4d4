package store

import (
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func TestSnapshotRestoresAggregateAndIdempotencyResult(t *testing.T) {
	dir := t.TempDir()
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	in, err := domain.NewIncident("persisted", "库房甲", "纸本文物", "高", now, []domain.EnvironmentalReading{{ID: "r", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: now}}, domain.RiskHigh, []string{"湿度超标"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	record := domain.RequestRecord{RequestID: "persisted-request", Operation: "create", IncidentID: in.ID, Digest: "digest", SuccessRevision: in.Revision, Result: in}
	if err = repo.Commit(in, 0, record); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(in.ID)
	if err != nil || loaded.Revision != 1 {
		t.Fatalf("聚合未恢复: %#v %v", loaded, err)
	}
	restored, ok := reopened.FindRequest(record.RequestID)
	if !ok || restored.Operation != "create" || restored.SuccessRevision != 1 || restored.Result == nil || restored.Result.ID != in.ID {
		t.Fatalf("幂等结果未恢复: %#v", restored)
	}
}
