package workflow

import (
	"errors"
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func TestPendingIncidentBaselineBackfillReassessesAtomically(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	in := createRefillIncident(t, svc, "baseline-refill", "库房A", "高", 70)
	if len(in.MissingBaselines) != 1 || in.MissingBaselines[0] != "湿度" {
		t.Fatalf("登记结果未保留缺失基线: %#v", in.MissingBaselines)
	}
	baseline := domain.EnvironmentalReading{
		ID: "humidity-baseline", Phase: domain.PhaseBaseline, Metric: "humidity", Value: 50, Unit: "%",
		MeasuredAt: now.Add(-2 * time.Hour), SourceNote: "历史监测记录", EvidenceRef: "baseline-evidence",
		EvidenceRecordedAt: now.Add(-time.Hour),
	}
	updated, err := svc.AddObservation(in.ID, in.Revision, []domain.EnvironmentalReading{baseline}, "", "保管员", "baseline-request")
	if err != nil {
		t.Fatalf("基线补录失败: %v", err)
	}
	if updated.Revision != in.Revision+1 || len(updated.MissingBaselines) != 0 {
		t.Fatalf("补录后的修订或缺失列表错误: rev=%d missing=%#v", updated.Revision, updated.MissingBaselines)
	}
	if len(updated.BaselinePairings) != 1 || updated.BaselinePairings[0].Status != "paired" || updated.BaselinePairings[0].BaselineReadingID != baseline.ID {
		t.Fatalf("基线配对未完成: %#v", updated.BaselinePairings)
	}
	if len(updated.Readings) != 2 || updated.Readings[0].ID != in.Readings[0].ID || updated.Timeline[len(updated.Timeline)-1].EventType != "基线补录" {
		t.Fatalf("原始读数或审计链未保留: %#v", updated)
	}
	if updated.Deadline.LatestResponseAt != updated.ObservedAt.Add(updated.ResponseDue) || len(updated.Comparisons) != 1 || updated.Comparisons[0].BaselineReadingID != baseline.ID {
		t.Fatalf("期限或读数比较未刷新: %#v", updated)
	}
	retried, err := svc.AddObservation(in.ID, in.Revision, []domain.EnvironmentalReading{baseline}, "", "保管员", "baseline-request")
	if err != nil || retried.Revision != updated.Revision || len(retried.Timeline) != len(updated.Timeline) {
		t.Fatalf("相同请求重试不幂等: %#v %v", retried, err)
	}
}

func TestBaselineBackfillRejectsWholeRequest(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	in := createRefillIncident(t, svc, "baseline-reject", "库房A", "高", 70)
	valid := domain.EnvironmentalReading{ID: "valid-baseline", Phase: domain.PhaseBaseline, Metric: "湿度", Value: 50, Unit: "%RH", MeasuredAt: now.Add(-time.Hour), SourceNote: "历史记录", EvidenceRef: "valid-baseline-evidence", EvidenceRecordedAt: now.Add(-30 * time.Minute)}
	late := valid
	late.ID, late.EvidenceRef, late.MeasuredAt = "late-baseline", "late-evidence", in.ObservedAt
	late.EvidenceRecordedAt = now
	_, err := svc.AddObservation(in.ID, in.Revision, []domain.EnvironmentalReading{valid, late}, "", "保管员", "late-request")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Field != "readings[1].measured_at" {
		t.Fatalf("晚基线错误字段不准确: %v", err)
	}
	stored, _ := svc.Repo.Get(in.ID)
	if stored.Revision != in.Revision || len(stored.Readings) != len(in.Readings) || len(stored.Timeline) != len(in.Timeline) {
		t.Fatalf("失败补录产生部分写入: %#v", stored)
	}
	duplicateEvidence := valid
	duplicateEvidence.ID, duplicateEvidence.EvidenceRef = "duplicate-evidence", in.Readings[0].EvidenceRef
	if _, err = svc.AddObservation(in.ID, in.Revision, []domain.EnvironmentalReading{duplicateEvidence}, "", "保管员", "duplicate-request"); err == nil {
		t.Fatal("复用登记证据的补录应失败")
	}
	assigned, err := svc.Assign(in.ID, in.Revision, "执行人", now.Add(time.Hour), "控湿", []domain.MitigationItem{{ID: "item", Description: "启动除湿"}}, "负责人", "assign-baseline-reject")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AddObservation(in.ID, assigned.Revision, []domain.EnvironmentalReading{valid}, "", "保管员", "state-request"); !errors.Is(err, domain.ErrState) {
		t.Fatalf("处置中事件应拒绝基线补录: %v", err)
	}
}

func TestAffectedItemFiltersAndStatistics(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	create := func(id, area, collectionID, material, sensitivity string, quantity int) {
		t.Helper()
		observed := now.Add(-10 * time.Minute)
		_, err := svc.Create(CreateCommand{
			ID: id, AreaID: area, Sensitivity: sensitivity, Actor: "保管员", RequestID: "create-" + id,
			ObservedAt: observed, SubmittedAt: now,
			AffectedItems: []domain.AffectedCollectionItem{{CollectionID: collectionID, Material: material, Quantity: quantity, Sensitivity: sensitivity}},
			Readings:      []domain.EnvironmentalReading{{ID: id + "-humidity", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: observed, SourceNote: "传感器", EvidenceRef: id + "-evidence", EvidenceRecordedAt: observed}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("paper", "库房A", "C-001", "纸质", "高", 2)
	create("metal", "库房B", "C-002", "金属", "中", 1)

	result := svc.List(domain.IncidentFilter{CollectionID: "C-001"})
	if len(result.Incidents) != 1 || result.Incidents[0].ID != "paper" || result.Statistics.MatchingIncidents != 1 || result.Statistics.AffectedItemRows != 1 || result.Statistics.AffectedQuantity != 2 || result.Statistics.ByMaterial["纸质"] != 2 {
		t.Fatalf("藏品编号筛选或汇总错误: %#v", result)
	}
	result = svc.List(domain.IncidentFilter{Material: "金属", ItemSensitivity: "中"})
	if len(result.Incidents) != 1 || result.Incidents[0].ID != "metal" || result.Statistics.ByMaterial["金属"] != 1 {
		t.Fatalf("材质与敏感级别交集筛选错误: %#v", result)
	}
	filter := domain.IncidentFilter{Status: domain.StatusPending, AreaID: "库房A", CollectionID: "C-999"}
	result = svc.List(filter)
	if len(result.Incidents) != 0 || result.Statistics.Total != 0 || result.Statistics.AffectedItemRows != 0 || result.Statistics.AffectedQuantity != 0 || len(result.Statistics.ByMaterial) != 0 || result.Statistics.Filters != filter {
		t.Fatalf("零结果或筛选回显错误: %#v", result)
	}
}
