package workload_preview_stale_test

import (
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestWorkloadPreviewRefreshesAfterAssignment(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repo := domain.NewMemoryRepo()
	service := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}

	first := pendingIncident(t, "workload-a", now)
	second := pendingIncident(t, "workload-b", now)
	if err := repo.Save(first, 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(second, 0); err != nil {
		t.Fatal(err)
	}

	candidate := domain.AssignmentCandidate{
		ID: "candidate-b", Assignee: "执行人甲", DueAt: now.Add(time.Hour), Summary: "调整展柜环境", SelectionReason: "熟悉展柜温控设备",
		Items: []domain.MitigationItem{{ID: "item-b", Description: "降低展柜温度", CoveredMetrics: []string{"温度"}}},
	}
	alternative := domain.AssignmentCandidate{
		ID: "candidate-c", Assignee: "执行人乙", DueAt: now.Add(2 * time.Hour), Summary: "转移敏感藏品", SelectionReason: "熟悉藏品转移流程",
		Items: []domain.MitigationItem{{ID: "item-c", Description: "转移敏感藏品", CoveredMetrics: []string{"温度"}}},
	}
	candidates := []domain.AssignmentCandidate{candidate, alternative}
	preview, err := service.PreviewAssignment(second.ID, second.Revision, candidates)
	if err != nil {
		t.Fatal(err)
	}
	initial := candidateResult(t, preview, candidate.ID)
	if !initial.Valid {
		t.Fatalf("初始工作量预览应无冲突: %#v", preview.Results)
	}

	_, err = service.Assign(
		first.ID, first.Revision, "执行人甲", now.Add(time.Hour), "调整库房环境",
		[]domain.MitigationItem{{ID: "item-a", Description: "降低库房温度", CoveredMetrics: []string{"温度"}}},
		"负责人", "assign-workload-a",
	)
	if err != nil {
		t.Fatalf("建立执行人活动任务失败: %v", err)
	}

	preview, err = service.PreviewAssignment(second.ID, second.Revision, candidates)
	if err != nil {
		t.Fatal(err)
	}
	stale := candidateResult(t, preview, candidate.ID)
	if stale.Valid || len(stale.WorkloadIssues) == 0 {
		t.Fatalf("成功分派后仍复用了无冲突的工作量预览: %#v", preview.Results)
	}
}

func candidateResult(t *testing.T, preview domain.AssignmentPreview, id string) domain.AssignmentCandidateResult {
	t.Helper()
	for _, result := range preview.Results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("候选方案 %s 缺少预检结果: %#v", id, preview.Results)
	return domain.AssignmentCandidateResult{}
}

func pendingIncident(t *testing.T, id string, now time.Time) *domain.PreservationIncident {
	t.Helper()
	observed := now.Add(-time.Hour)
	incident, err := domain.NewIncident(
		id, "库房A", "纸质藏品", "高", observed,
		[]domain.EnvironmentalReading{{
			ID: id + "-temperature", Phase: domain.PhaseAbnormal, Metric: "温度", Value: 35, Unit: "℃",
			MeasuredAt: observed, SourceNote: "现场监测仪", EvidenceRef: id + "-evidence", EvidenceRecordedAt: observed,
		}},
		domain.RiskHigh, []string{"温度高于阈值"}, 4*time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	return incident
}
