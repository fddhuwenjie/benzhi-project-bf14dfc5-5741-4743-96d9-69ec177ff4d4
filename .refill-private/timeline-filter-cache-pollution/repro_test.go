package timelinefiltercachepollution_test

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestLongTimelineFiltersAreIsolated(t *testing.T) {
	const incidentID = "incident-long-timeline"
	events := make([]domain.IncidentEvent, 32)
	for index := range events {
		sequence := index + 1
		eventType := "现场观测"
		if sequence%2 == 0 {
			eventType = "人工复核"
		}
		events[index] = domain.IncidentEvent{
			ID:         fmt.Sprintf("%s-%d", incidentID, sequence),
			IncidentID: incidentID,
			Sequence:   sequence,
			EventType:  eventType,
			Actor:      "测试人员",
			OccurredAt: time.Date(2026, 8, 24, 8, 0, sequence, 0, time.UTC),
		}
	}

	repo := domain.NewMemoryRepo()
	incident := &domain.PreservationIncident{
		ID:         incidentID,
		Status:     domain.StatusPending,
		Revision:   32,
		ObservedAt: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
		Timeline:   events,
	}
	if err := repo.Commit(incident, 0, domain.RequestRecord{}); err != nil {
		t.Fatalf("准备长时间线失败: %v", err)
	}
	svc := &workflow.Service{
		Repo:  repo,
		Rules: assessment.DefaultRules(),
		Now:   func() time.Time { return time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC) },
	}

	first, err := svc.GetTimeline(incidentID, workflow.TimelineFilter{EventType: "现场观测"})
	if err != nil {
		t.Fatalf("首次筛选失败: %v", err)
	}
	if len(first.Timeline) != 16 || first.TimelinePage == nil || first.TimelinePage.Total != 16 {
		t.Fatalf("首次筛选应返回 16 条且总数为 16，实际为 %d，分页信息为 %#v", len(first.Timeline), first.TimelinePage)
	}

	second, err := svc.GetTimeline(incidentID, workflow.TimelineFilter{EventType: "人工复核"})
	if err != nil {
		t.Fatalf("第二次筛选失败: %v", err)
	}
	if len(second.Timeline) != 16 || second.TimelinePage == nil || second.TimelinePage.Total != 16 {
		t.Fatalf("TestLongTimelineFiltersAreIsolated: 第二次筛选应独立读取完整时间线并返回 16 条且总数为 16，实际为 %d，分页信息为 %#v", len(second.Timeline), second.TimelinePage)
	}
}
