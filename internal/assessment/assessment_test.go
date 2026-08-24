package assessment

import (
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func TestContinuousDurationAndRecovery(t *testing.T) {
	now := time.Now().UTC()
	reading := func(id string, at time.Time, value float64, unit string) domain.EnvironmentalReading {
		return domain.EnvironmentalReading{ID: id, Phase: domain.PhaseAbnormal, Metric: "湿度", Value: value, Unit: unit, MeasuredAt: at, SourceNote: "记录仪", EvidenceRef: "ev-" + id, EvidenceRecordedAt: at}
	}
	readings := []domain.EnvironmentalReading{
		reading("old", now.Add(-8*time.Hour), 72, "%"),
		reading("recovered", now.Add(-7*time.Hour), 50, "%RH"),
		reading("start", now.Add(-6*time.Hour), 70, "%RH"),
		reading("end", now, 70, "%"),
	}
	result, err := EvaluateAt(readings, "高", now, now, DefaultRules())
	if err != nil {
		t.Fatal(err)
	}
	if result.Level != domain.RiskCritical || result.Response != 4*time.Hour {
		t.Fatalf("风险结果 = %s/%s", result.Level, result.Response)
	}
	if len(result.Intervals) != 2 || result.Intervals[1].Duration != 6*time.Hour {
		t.Fatalf("连续区间 = %#v", result.Intervals)
	}
	if result.Intervals[1].ReadingIDs[0] != "start" {
		t.Fatalf("恢复读数后未重新计时: %#v", result.Intervals[1])
	}
}

func TestEvidenceAndMeasurementValidation(t *testing.T) {
	now := time.Now().UTC()
	readings := []domain.EnvironmentalReading{
		{ID: "a", Phase: domain.PhaseAbnormal, Metric: "温度", Value: 35, Unit: "℃", MeasuredAt: now, SourceNote: "仪表", EvidenceRef: "same", EvidenceRecordedAt: now},
		{ID: "b", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: now, SourceNote: "仪表", EvidenceRef: "same", EvidenceRecordedAt: now},
	}
	if _, err := EvaluateAt(readings, "高", now, now, DefaultRules()); err == nil {
		t.Fatal("重复证据引用应被拒绝")
	}
	readings[1].EvidenceRef = "other"
	readings[1].MeasuredAt = now.Add(time.Minute)
	if _, err := EvaluateAt(readings, "高", now, now, DefaultRules()); err == nil {
		t.Fatal("晚于登记的测量时间应被拒绝")
	}
}
