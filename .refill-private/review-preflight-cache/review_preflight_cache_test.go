package reviewpreflightcache_test

import (
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestReviewPreflightRefreshesAfterReadingCorrection(t *testing.T) {
	now := time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC)
	repo := domain.NewMemoryRepo()
	svc := &workflow.Service{
		Repo:  repo,
		Rules: assessment.DefaultRules(),
		Now:   func() time.Time { return now },
	}

	incident, err := svc.Create(workflow.CreateCommand{
		ID:            "review-cache-incident",
		AreaID:        "库房甲",
		AffectedScope: "纸本文物与油画",
		Sensitivity:   "高",
		Actor:         "保管员",
		RequestID:     "review-cache-create",
		ObservedAt:    now,
		SubmittedAt:   now,
		Readings: []domain.EnvironmentalReading{
			reading("abnormal-temperature", domain.PhaseAbnormal, "温度", 35, "℃", "temperature-registration", now),
			reading("abnormal-humidity", domain.PhaseAbnormal, "湿度", 75, "%RH", "humidity-registration", now),
		},
	})
	if err != nil {
		t.Fatalf("创建双指标事件失败: %v", err)
	}
	incident, err = svc.Assign(
		incident.ID,
		incident.Revision,
		"执行人",
		now.Add(time.Hour),
		"同步调节温湿度",
		[]domain.MitigationItem{{ID: "climate-control", Description: "调节温湿度"}},
		"负责人",
		"review-cache-assign",
	)
	if err != nil {
		t.Fatalf("分派失败: %v", err)
	}
	incident, err = svc.RecordReadings(
		incident.ID,
		incident.Revision,
		"climate-control",
		"完成首次调节",
		[]domain.EnvironmentalReading{
			reading("effect-temperature-old", "", "温度", 22, "℃", "temperature-effect-old", now),
			reading("effect-humidity-old", "", "湿度", 50, "%RH", "humidity-effect-old", now),
		},
		"执行人",
		"review-cache-record",
	)
	if err != nil {
		t.Fatalf("记录首次效果读数失败: %v", err)
	}

	first, err := svc.ReviewPreflight(incident.ID, incident.Revision)
	if err != nil {
		t.Fatalf("首次复核预检失败: %v", err)
	}
	incident, err = svc.CorrectReadings(
		incident.ID,
		incident.Revision,
		"climate-control",
		"使用校准设备重新测量",
		"原效果读数来自失准设备",
		[]domain.EnvironmentalReading{
			reading("effect-temperature-new", "", "温度", 21, "℃", "temperature-effect-new", now),
			reading("effect-humidity-new", "", "湿度", 48, "%RH", "humidity-effect-new", now),
		},
		"执行人",
		"review-cache-correct",
	)
	if err != nil {
		t.Fatalf("更正效果读数失败: %v", err)
	}

	second, err := svc.ReviewPreflight(incident.ID, incident.Revision)
	if err != nil {
		t.Fatalf("更正后的复核预检失败: %v", err)
	}
	if second.Revision != incident.Revision {
		t.Fatalf("复核预检返回旧 revision: got %d want %d", second.Revision, incident.Revision)
	}
	if contains(second.ReadingIDs, "effect-temperature-old") || contains(second.ReadingIDs, "effect-humidity-old") {
		t.Fatalf("复核预检复用了已失效的效果读数: %v", second.ReadingIDs)
	}
	if !contains(second.ReadingIDs, "effect-temperature-new") || !contains(second.ReadingIDs, "effect-humidity-new") {
		t.Fatalf("复核预检未包含更正后的效果读数: %v", second.ReadingIDs)
	}
	if second.Checksum == first.Checksum {
		t.Fatalf("证据集合变化后 checksum 未更新: %s", second.Checksum)
	}
}

func reading(id string, phase domain.ReadingPhase, metric string, value float64, unit, evidence string, measuredAt time.Time) domain.EnvironmentalReading {
	return domain.EnvironmentalReading{
		ID:                 id,
		Phase:              phase,
		Metric:             metric,
		Value:              value,
		Unit:               unit,
		MeasuredAt:         measuredAt,
		SourceNote:         "校准监测设备",
		EvidenceRef:        evidence,
		EvidenceRecordedAt: measuredAt,
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
