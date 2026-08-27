package archivefilterscope_test

import (
	"testing"
	"time"

	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
)

type archiveRepo struct {
	domain.Repository
	incidents []*domain.PreservationIncident
}

func (r *archiveRepo) List(domain.IncidentFilter) []*domain.PreservationIncident {
	return r.incidents
}

func closedArchive(id, metric string) *domain.PreservationIncident {
	now := time.Date(2026, 1, 10, 8, 0, 0, 0, time.UTC)
	in := &domain.PreservationIncident{
		ID:            id,
		AreaID:        "库房甲",
		AffectedScope: "书画",
		Sensitivity:   "中",
		Status:        domain.StatusClosed,
		ObservedAt:    now.Add(-time.Hour),
		CreatedAt:     now.Add(-time.Hour),
		Readings: []domain.EnvironmentalReading{{
			ID: "reading-" + id, IncidentID: id, Phase: domain.PhaseAbnormal,
			Metric: metric, Value: 1, Unit: "℃", MeasuredAt: now.Add(-time.Hour),
			EvidenceRef: "evidence-" + id,
		}},
	}
	if err := in.FreezeArchive(now); err != nil {
		panic(err)
	}
	return in
}

func TestArchiveSearchSeparatesMetricFilters(t *testing.T) {
	temp := closedArchive("incident-temp", "温度")
	humidity := closedArchive("incident-humidity", "湿度")
	svc := &workflow.Service{Repo: &archiveRepo{incidents: []*domain.PreservationIncident{temp, humidity}}}

	first, err := svc.SearchArchive(workflow.ArchiveFilter{Metric: "温度"})
	if err != nil || len(first) != 1 || first[0].IncidentID != temp.ID {
		t.Fatalf("温度筛选结果异常: %#v, %v", first, err)
	}
	second, err := svc.SearchArchive(workflow.ArchiveFilter{Metric: "湿度"})
	if err != nil {
		t.Fatalf("湿度筛选失败: %v", err)
	}
	if len(second) != 1 || second[0].IncidentID != humidity.ID {
		t.Fatalf("不同指标查询复用了旧归档结果: %#v", second)
	}
}
