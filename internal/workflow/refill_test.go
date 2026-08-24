package workflow

import (
	"errors"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func refillService(now time.Time) *Service {
	return &Service{Repo: domain.NewMemoryRepo(), Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
}

func createRefillIncident(t *testing.T, svc *Service, id, area, sensitivity string, value float64) *domain.PreservationIncident {
	t.Helper()
	now := svc.now()
	observed := now.Add(-10 * time.Minute)
	itemSensitivity := sensitivity
	in, err := svc.Create(CreateCommand{
		ID: id, AreaID: area, Sensitivity: sensitivity, Actor: "保管员", RequestID: "create-" + id,
		ObservedAt: observed, SubmittedAt: now,
		AffectedItems: []domain.AffectedCollectionItem{{CollectionID: id + "-collection", Material: "纸质", Quantity: 1, Sensitivity: itemSensitivity, ImpactNote: "受潮"}},
		Readings:      []domain.EnvironmentalReading{{ID: id + "-humidity", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: value, Unit: "%RH", MeasuredAt: observed, SourceNote: "库房传感器", EvidenceRef: id + "-evidence", EvidenceRecordedAt: observed}},
	})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	return in
}

func TestAffectedItemsAndSupplementalObservation(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	in := createRefillIncident(t, svc, "incident-observation", "库房A", "高", 65)
	if in.AffectedScope == "" || len(in.AffectedItems) != 1 || len(in.SensitivityTriggers) != 1 {
		t.Fatalf("结构化藏品清单未冻结: %#v", in)
	}
	oldRisk := in.RiskLevel
	readingAt := now.Add(-5 * time.Minute)
	updated, err := svc.AddObservation(in.ID, in.Revision, []domain.EnvironmentalReading{{ID: "humidity-severe", Metric: "湿度", Value: 90, Unit: "%RH", MeasuredAt: readingAt, SourceNote: "复测", EvidenceRef: "evidence-severe", EvidenceRecordedAt: readingAt}}, "同一区域复测", "保管员", "observation-1")
	if err != nil {
		t.Fatalf("补充观测失败: %v", err)
	}
	if oldRisk != domain.RiskMedium || updated.RiskLevel != domain.RiskCritical || updated.Revision != in.Revision+1 {
		t.Fatalf("风险未升级: %s -> %s, revision=%d", oldRisk, updated.RiskLevel, updated.Revision)
	}
	if len(updated.AdditionalObservations) != 1 || updated.Timeline[len(updated.Timeline)-2].EventType != "补充观测" || updated.Timeline[len(updated.Timeline)-1].EventType != "风险变更" {
		t.Fatalf("补充观测时间线不完整: %#v", updated.Timeline)
	}
	_, err = svc.AddObservation(in.ID, updated.Revision, []domain.EnvironmentalReading{{ID: "humidity-again", Metric: "湿度", Value: 92, Unit: "%RH", MeasuredAt: readingAt, SourceNote: "复测", EvidenceRef: "evidence-severe", EvidenceRecordedAt: readingAt}}, "再次复测", "保管员", "observation-2")
	if err == nil {
		t.Fatal("复用证据应失败")
	}
	stored, _ := svc.Repo.Get(in.ID)
	if stored.Revision != updated.Revision || len(stored.AdditionalObservations) != 1 {
		t.Fatalf("失败请求留下了部分数据: %#v", stored)
	}
}

func TestAffectedItemsRowValidation(t *testing.T) {
	_, _, _, err := domain.ValidateAffectedItems([]domain.AffectedCollectionItem{{CollectionID: "A", Material: "陶器", Quantity: 1, Sensitivity: "中"}, {CollectionID: "A", Material: "纸质", Quantity: 1, Sensitivity: "高"}}, "高")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Field != "affected_items[1].collection_id" {
		t.Fatalf("重复编号错误字段不准确: %v", err)
	}
}

func TestAssignmentComparisonAndBatchAtomicity(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	in := createRefillIncident(t, svc, "candidate", "库房C", "低", 70)
	candidates := []domain.AssignmentCandidate{
		{ID: "missing", Assignee: "执行人甲", DueAt: now.Add(time.Hour), Summary: "巡检", SelectionReason: "现场巡查", Items: []domain.MitigationItem{{ID: "m1", Description: "巡检"}}},
		{ID: "complete", Assignee: "执行人乙", DueAt: now.Add(time.Hour), Summary: "控湿", SelectionReason: "覆盖全部异常", Items: []domain.MitigationItem{{ID: "m2", Description: "启动除湿", CoveredMetrics: []string{"湿度"}}}},
	}
	preview, err := svc.PreviewAssignment(in.ID, in.Revision, candidates)
	validByID := map[string]bool{}
	for _, result := range preview.Results {
		validByID[result.ID] = result.Valid
	}
	if err != nil || validByID["missing"] || !validByID["complete"] {
		t.Fatalf("方案比选结果错误: %#v %v", preview, err)
	}
	_, err = svc.ConfirmAssignmentCandidate(in.ID, in.Revision, candidates, "complete", preview.Checksum+"x", "负责人", "candidate-confirm-bad")
	if err == nil {
		t.Fatal("篡改候选方案应使预览失效")
	}
	assigned, err := svc.ConfirmAssignmentCandidate(in.ID, in.Revision, candidates, "complete", preview.Checksum, "负责人", "candidate-confirm")
	if err != nil || assigned.Status != domain.StatusMitigating || len(assigned.AssignmentCandidates) != 2 {
		t.Fatalf("确认候选方案失败: %#v %v", assigned, err)
	}

	a := createRefillIncident(t, svc, "batch-a", "库房BA", "低", 70)
	b := createRefillIncident(t, svc, "batch-b", "库房BB", "低", 70)
	_, err = svc.Assign(b.ID, b.Revision, "其他执行人", now.Add(time.Hour), "控湿", []domain.MitigationItem{{ID: "old", Description: "湿度处理", CoveredMetrics: []string{"湿度"}}}, "负责人", "assign-b")
	if err != nil {
		t.Fatalf("准备并发状态失败: %v", err)
	}
	command := BatchAssignmentCommand{Entries: []BatchAssignmentEntry{{IncidentID: a.ID, ExpectedRevision: a.Revision}, {IncidentID: b.ID, ExpectedRevision: b.Revision}}, Assignee: "批量执行人", DueAt: now.Add(time.Hour), Summary: "标准控湿", Items: []domain.MitigationItem{{ID: "template", Description: "湿度处理", CoveredMetrics: []string{"湿度"}}}, Actor: "负责人", RequestID: "batch-fail"}
	_, err = svc.AssignBatch(command)
	var batchConflict *domain.BatchConflictError
	if !errors.As(err, &batchConflict) {
		t.Fatalf("应返回批量冲突: %v", err)
	}
	unchanged, _ := svc.Repo.Get(a.ID)
	if unchanged.Status != domain.StatusPending || unchanged.Revision != a.Revision {
		t.Fatalf("批量失败出现部分分派: %#v", unchanged)
	}
	c := createRefillIncident(t, svc, "batch-c", "库房BC", "低", 70)
	d := createRefillIncident(t, svc, "batch-d", "库房BD", "低", 70)
	successCommand := BatchAssignmentCommand{Entries: []BatchAssignmentEntry{{IncidentID: c.ID, ExpectedRevision: c.Revision}, {IncidentID: d.ID, ExpectedRevision: d.Revision}}, Assignee: "批量执行人", DueAt: now.Add(time.Hour), Summary: "标准控湿", Items: []domain.MitigationItem{{ID: "template", Description: "湿度处理", CoveredMetrics: []string{"湿度"}}}, Actor: "负责人", RequestID: "batch-success"}
	first, err := svc.AssignBatch(successCommand)
	if err != nil || len(first.Incidents) != 2 {
		t.Fatalf("批量分派失败: %#v %v", first, err)
	}
	effectAt := now
	_, err = svc.RecordReadings(c.ID, 2, "template", "完成控湿", []domain.EnvironmentalReading{{ID: "batch-c-effect", Metric: "湿度", Value: 50, Unit: "%RH", MeasuredAt: effectAt, SourceNote: "复测", EvidenceRef: "batch-c-effect-evidence", EvidenceRecordedAt: effectAt}}, "批量执行人", "batch-c-complete")
	if err != nil {
		t.Fatalf("推进批量事件失败: %v", err)
	}
	replayed, err := svc.AssignBatch(successCommand)
	if err != nil || replayed.Results[0].Revision != 2 || replayed.Results[1].Revision != 2 {
		t.Fatalf("批量幂等未返回首次结果: %#v %v", replayed, err)
	}
}

func TestPlanLedgerDeadlineAndMetricVerification(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	svc := refillService(now)
	in := createRefillIncident(t, svc, "lifecycle", "库房L", "低", 70)
	assigned, err := svc.Assign(in.ID, in.Revision, "执行人", now.Add(time.Hour), "控湿", []domain.MitigationItem{{ID: "humidity", Description: "控制湿度", CoveredMetrics: []string{"湿度"}}}, "负责人", "assign-life")
	if err != nil {
		t.Fatalf("分派失败: %v", err)
	}
	changed, err := svc.ChangePlan(in.ID, assigned.Revision, domain.PlanChange{Add: []domain.MitigationItem{{ID: "inspect", Description: "检查设备"}}}, "现场增加检查", "负责人", "plan-change")
	if err != nil || len(changed.Plan.Items) != 2 {
		t.Fatalf("方案变更失败: %#v %v", changed, err)
	}
	requested, err := svc.RequestDeadlineChange(in.ID, changed.Revision, now.Add(2*time.Hour), "设备等待", []string{"humidity"}, "执行人", "deadline-request")
	if err != nil || requested.DueAt != assigned.DueAt || requested.PendingDeadlineChange == nil {
		t.Fatalf("延期申请错误修改当前期限: %#v %v", requested, err)
	}
	approved, err := svc.DecideDeadlineChange(in.ID, requested.Revision, true, "负责人", "同意延期", "deadline-decision")
	if err != nil || !approved.DueAt.Equal(now.Add(2*time.Hour)) || approved.DeadlineChangeCount != 1 {
		t.Fatalf("延期审批失败: %#v %v", approved, err)
	}
	recorded, err := svc.AddProcessRecord(in.ID, approved.Revision, "humidity", domain.ProcessRecord{Type: "开始", OccurredAt: now, Note: "启动除湿机", EvidenceRef: "process-start"}, "执行人", "process-1")
	if err != nil || len(recorded.Plan.Items[0].ProcessRecords) != 1 || recorded.Plan.Items[0].Status == "已完成" {
		t.Fatalf("过程台账失败: %#v %v", recorded, err)
	}
	effect := domain.EnvironmentalReading{ID: "humidity-effect", Metric: "湿度", Value: 50, Unit: "%RH", MeasuredAt: now, SourceNote: "效果复测", EvidenceRef: "effect-evidence", EvidenceRecordedAt: now}
	completed, err := svc.CompleteItemWithRecords(in.ID, recorded.Revision, "humidity", "湿度恢复", []domain.EnvironmentalReading{effect}, []int{1}, "执行人", "complete-humidity")
	if err != nil || completed.Plan.Items[0].Status != "已完成" {
		t.Fatalf("引用过程记录完成失败: %#v %v", completed, err)
	}
	// 新增检查项不覆盖异常指标，但仍需完成后才能提交复核。
	start2, err := svc.AddProcessRecord(in.ID, completed.Revision, "inspect", domain.ProcessRecord{Type: "开始", OccurredAt: now, Note: "开始检查", EvidenceRef: "inspect-start"}, "执行人", "process-2")
	if err != nil {
		t.Fatal(err)
	}
	inspectEffect := domain.EnvironmentalReading{ID: "inspect-effect", Metric: "湿度", Value: 50, Unit: "%RH", MeasuredAt: now, SourceNote: "检查复测", EvidenceRef: "inspect-effect-evidence", EvidenceRecordedAt: now}
	completed, err = svc.CompleteItemWithRecords(in.ID, start2.Revision, "inspect", "检查完成", []domain.EnvironmentalReading{inspectEffect}, []int{1}, "执行人", "complete-inspect")
	if err != nil {
		t.Fatal(err)
	}
	review, err := svc.Submit(in.ID, completed.Revision, "执行人", "submit-review-life")
	if err != nil {
		t.Fatalf("提交复核失败: %v", err)
	}
	confirmed := []string{"lifecycle-humidity", "inspect-effect"}
	closed, err := svc.VerifyMetrics(in.ID, review.Revision, "复核人", "合格", "读数和现场均恢复", "metric-verification", []domain.MetricVerification{{Metric: "湿度", Decision: "合格", ConfirmedReadingIDs: confirmed, Note: "湿度稳定", EvidenceRef: "inspect-effect-evidence"}})
	if err != nil || closed.Status != domain.StatusClosed || closed.Archive == nil || len(closed.Verification.MetricDecisions) != 1 {
		t.Fatalf("逐指标复核关闭失败: %#v %v", closed, err)
	}
}
