package listprojectionstale_test

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestLargeListProjectionInvalidatedAfterAssignment(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repo := domain.NewMemoryRepo()
	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}

	for n := 0; n < 64; n++ {
		id := fmt.Sprintf("projection-%02d", n)
		observed := now.Add(-10 * time.Minute)
		reading := domain.EnvironmentalReading{
			ID: id + "-reading", Phase: domain.PhaseAbnormal, Metric: "温度", Value: 35, Unit: "℃",
			MeasuredAt: observed, SourceNote: "库房传感器", EvidenceRef: id + "-evidence", EvidenceRecordedAt: observed,
		}
		incident, err := domain.NewIncident(id, fmt.Sprintf("库房-%02d", n), "测试藏品", "低", observed, []domain.EnvironmentalReading{reading}, domain.RiskLow, []string{"温度越界"}, 4*time.Hour)
		if err != nil {
			t.Fatalf("构造事件失败: %v", err)
		}
		incident.SetRegistrationDetails(nil)
		if err = repo.Commit(incident, 0, domain.RequestRecord{}); err != nil {
			t.Fatalf("准备事件失败: %v", err)
		}
	}

	first := svc.List(domain.IncidentFilter{})
	before := findIncident(first.Incidents, "projection-00")
	if before == nil || before.Status != domain.StatusPending || before.Revision != 1 {
		t.Fatalf("缓存建立前状态异常: %#v", before)
	}

	assigned, err := svc.Assign(
		"projection-00", before.Revision, "执行人", now.Add(time.Hour), "降低展柜温度",
		[]domain.MitigationItem{{ID: "cooling", Description: "启动温控设备"}}, "负责人", "assign-projection-00",
	)
	if err != nil {
		t.Fatalf("分派失败: %v", err)
	}
	if assigned.Status != domain.StatusMitigating || assigned.Revision != 2 {
		t.Fatalf("分派没有更新聚合: status=%s revision=%d", assigned.Status, assigned.Revision)
	}

	second := svc.List(domain.IncidentFilter{})
	after := findIncident(second.Incidents, "projection-00")
	if after == nil {
		t.Fatal("分派后的列表缺少目标事件")
	}
	if after.Status != domain.StatusMitigating || after.Revision != assigned.Revision {
		t.Fatalf("成功分派后列表仍复用旧投影: status=%s revision=%d, want status=%s revision=%d", after.Status, after.Revision, assigned.Status, assigned.Revision)
	}
}

func findIncident(incidents []*domain.PreservationIncident, id string) *domain.PreservationIncident {
	for _, incident := range incidents {
		if incident.ID == id {
			return incident
		}
	}
	return nil
}
