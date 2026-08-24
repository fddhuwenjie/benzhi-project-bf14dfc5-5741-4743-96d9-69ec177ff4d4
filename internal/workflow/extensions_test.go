package workflow

import (
	"errors"
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func TestPreflightAggregatesErrorsWithoutPersistence(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	readings := []domain.EnvironmentalReading{
		{ID: "a", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "unsupported", MeasuredAt: now, SourceNote: "仪表", EvidenceRef: "same", EvidenceRecordedAt: now},
		{ID: "b", Phase: domain.PhaseAbnormal, Metric: "温度", Value: 95, Unit: "℉", MeasuredAt: now, SourceNote: "仪表", EvidenceRef: "same", EvidenceRecordedAt: now},
	}
	preview := svc.Preflight(CreateCommand{AreaID: "库房甲", AffectedScope: "纸本", Sensitivity: "高", ObservedAt: now.Add(time.Minute), SubmittedAt: now, Readings: readings})
	if preview.Valid || len(preview.Errors) < 3 {
		t.Fatalf("预检应聚合时间、单位和证据错误: %#v", preview.Errors)
	}
	if len(preview.NormalizedReadings) != 1 || preview.NormalizedReadings[0].Unit != "℃" || preview.NormalizedReadings[0].Value != 35 {
		t.Fatalf("合法字段未保留换算预览: %#v", preview.NormalizedReadings)
	}
	if len(repo.List(domain.IncidentFilter{})) != 0 {
		t.Fatal("预检不得持久化事件")
	}
}

func TestSourceDuplicateAndRelatedConfirmation(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	first := createHumidityIncident(t, svc, now, "source-1", 70)
	duplicateReading := domain.EnvironmentalReading{ID: "source-2-h", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: now, SourceNote: "仪表", EvidenceRef: first.Readings[0].EvidenceRef, EvidenceRecordedAt: now}
	_, err := svc.Create(CreateCommand{ID: "source-2", AreaID: first.AreaID, AffectedScope: first.AffectedScope, Sensitivity: "高", Actor: "保管员", RequestID: "source-2-create", ObservedAt: now, SubmittedAt: now, Readings: []domain.EnvironmentalReading{duplicateReading}})
	var candidate *domain.CandidateConflictError
	if !errors.As(err, &candidate) || candidate.Kind != "exact_duplicate" || len(repo.List(domain.IncidentFilter{})) != 1 {
		t.Fatalf("完全重复未阻止登记: %v", err)
	}
	relatedReading := duplicateReading
	relatedReading.ID, relatedReading.EvidenceRef = "source-3-h", "source-3-evidence"
	_, err = svc.Create(CreateCommand{ID: "source-3", AreaID: first.AreaID, AffectedScope: "油画", Sensitivity: "高", Actor: "保管员", RequestID: "source-3-create", ObservedAt: now, SubmittedAt: now, Readings: []domain.EnvironmentalReading{relatedReading}})
	if !errors.As(err, &candidate) || candidate.Kind != "related_confirmation_required" {
		t.Fatalf("关联事件未要求理由: %v", err)
	}
	created, err := svc.Create(CreateCommand{ID: "source-3", AreaID: first.AreaID, AffectedScope: "油画", Sensitivity: "高", Actor: "保管员", RequestID: "source-3-confirm", IndependentReason: "同区不同藏品需独立处置", ObservedAt: now, SubmittedAt: now, Readings: []domain.EnvironmentalReading{relatedReading}})
	if err != nil || created.IndependentReason == "" || len(created.RelatedCandidates) == 0 {
		t.Fatalf("确认独立登记失败: %#v %v", created, err)
	}
}

func TestDependencyCorrectionSeparationAndArchive(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	in := createHumidityIncident(t, svc, now, "extended", 72)
	items := []domain.MitigationItem{{ID: "isolate", Description: "隔离"}, {ID: "adjust", Description: "调节", PrerequisiteIDs: []string{"isolate"}}}
	in, err := svc.Assign(in.ID, in.Revision, "执行人", now.Add(time.Hour), "安全处置", items, "负责人", "extended-assign")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.RecordReadings(in.ID, in.Revision, "adjust", "调节", []domain.EnvironmentalReading{effect("blocked-effect", now, 60, "blocked-evidence")}, "执行人", "blocked-request")
	var blocked *domain.DependencyBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("依赖未阻塞: %v", err)
	}
	stored, _ := repo.Get(in.ID)
	if stored.Revision != in.Revision {
		t.Fatal("依赖失败改变了修订号")
	}
	in, err = svc.RecordReadings(in.ID, in.Revision, "isolate", "已隔离", []domain.EnvironmentalReading{effect("isolate-effect", now, 66, "isolate-evidence")}, "执行人", "isolate-request")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.RecordReadings(in.ID, in.Revision, "adjust", "已调节", []domain.EnvironmentalReading{effect("adjust-old", now, 52, "adjust-old-evidence")}, "执行人", "adjust-request")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.CorrectReadings(in.ID, in.Revision, "adjust", "复核后修正", "小数点录入错误", []domain.EnvironmentalReading{effect("adjust-new", now, 62, "adjust-new-evidence")}, "执行人", "correction-request")
	if err != nil || in.Comparisons[0].EffectReadingID != "adjust-new" {
		t.Fatalf("更正未替代有效读数: %#v %v", in.Comparisons, err)
	}
	in, err = svc.Submit(in.ID, in.Revision, "执行人", "extended-submit")
	if err != nil {
		t.Fatal(err)
	}
	ids := comparisonReadingIDs(in.Comparisons)
	if _, err = svc.VerifyConfirmed(in.ID, in.Revision, "执行人", "合格", "", "same-reviewer", ids); err == nil {
		t.Fatal("执行人不得复核关闭")
	}
	in, err = svc.VerifyConfirmed(in.ID, in.Revision, "独立复核人", "合格", "读数恢复", "extended-close", ids)
	if err != nil {
		t.Fatal(err)
	}
	if in.Archive == nil || in.Archive.Checksum == "" || in.Archive.ChecksumStatus != "有效" || len(in.Archive.EvidenceRefs) != 4 {
		t.Fatalf("归档摘要不完整: %#v", in.Archive)
	}
	retry, err := svc.VerifyConfirmed(in.ID, in.Revision-1, "独立复核人", "合格", "读数恢复", "extended-close", ids)
	if err != nil || retry.Archive.Checksum != in.Archive.Checksum || len(retry.Timeline) != len(in.Timeline) {
		t.Fatalf("关闭重试改变归档: %#v %v", retry, err)
	}
}

func TestTimelineFilterUsesSequenceCursor(t *testing.T) {
	now := time.Now().UTC()
	svc, _ := newTestService(now)
	in := assignOne(t, svc, now, createHumidityIncident(t, svc, now, "timeline", 70), "timeline-assign")
	in, err := svc.RecordReadings(in.ID, in.Revision, "item-1", "完成", []domain.EnvironmentalReading{effect("timeline-effect", now, 55, "timeline-evidence")}, "执行人", "timeline-item")
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.GetTimeline(in.ID, TimelineFilter{Actor: "执行人", Round: 1, Limit: 1})
	if err != nil || page.TimelinePage.Total != 1 || len(page.Timeline) != 1 || page.Timeline[0].Sequence != 3 || page.Timeline[0].RevisionBefore != 2 {
		t.Fatalf("时间线筛选追溯错误: %#v %v", page, err)
	}
}
