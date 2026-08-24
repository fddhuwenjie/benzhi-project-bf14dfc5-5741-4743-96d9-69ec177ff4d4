package workflow

import (
	"errors"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func registrationReading(id string, phase domain.ReadingPhase, metric string, value float64, unit string, measured time.Time) domain.EnvironmentalReading {
	return domain.EnvironmentalReading{ID: id, Phase: phase, Metric: metric, Value: value, Unit: unit, MeasuredAt: measured, SourceNote: "校准监测仪", EvidenceRef: id + "-evidence", EvidenceRecordedAt: measured}
}

func TestBaselinePairingAndLockedRuleHits(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	readings := []domain.EnvironmentalReading{
		registrationReading("base", domain.PhaseBaseline, "temperature", 68, "℉", now.Add(-7*time.Hour)),
		registrationReading("abnormal-1", domain.PhaseAbnormal, "温度", 35, "℃", now.Add(-6*time.Hour)),
		registrationReading("abnormal-2", domain.PhaseAbnormal, "温度", 35, "℃", now),
	}
	in, err := svc.Create(CreateCommand{ID: "paired", AreaID: "库房甲", AffectedScope: "纸本", Sensitivity: "高", Actor: "保管员", RequestID: "paired-create", ObservedAt: now, SubmittedAt: now, Readings: readings})
	if err != nil {
		t.Fatal(err)
	}
	if in.RuleSetVersion == "" || len(in.RuleHits) != 1 || in.RuleHits[0].Duration != 6*time.Hour || len(in.BaselinePairings) != 1 || in.BaselinePairings[0].BaselineReadingID != "base" {
		t.Fatalf("登记研判快照不完整: %#v %#v", in.RuleHits, in.BaselinePairings)
	}
	lockedRisk := in.RiskLevel
	svc.Rules.TempMax = 100
	got, err := svc.Get(in.ID)
	if err != nil || got.RiskLevel != lockedRisk || !got.RuleHits[0].Matched {
		t.Fatalf("当前规则覆盖了历史研判: %#v %v", got, err)
	}

	late := []domain.EnvironmentalReading{
		registrationReading("late-abnormal", domain.PhaseAbnormal, "温度", 35, "℃", now.Add(-time.Hour)),
		registrationReading("late-base", domain.PhaseBaseline, "温度", 20, "℃", now),
	}
	_, err = svc.Create(CreateCommand{ID: "late", AreaID: "库房乙", AffectedScope: "纸本", Sensitivity: "中", Actor: "保管员", RequestID: "late-create", ObservedAt: now, SubmittedAt: now, Readings: late})
	if err == nil || len(repo.List(domain.IncidentFilter{})) != 1 {
		t.Fatalf("晚于异常的基线未被原子拒绝: %v", err)
	}
}

func TestRegistrationCorrectionTransferAndAtomicBatch(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	in := createHumidityIncident(t, svc, now, "operations", 90)
	replacement := registrationReading("corrected-h", domain.PhaseAbnormal, "湿度", 66, "%", now)
	in, err := svc.CorrectRegistrationReading(in.ID, in.Revision, "operations-h", replacement, "原值录入错误", "保管员", "correct-reading")
	if err != nil || in.Revision != 2 || in.Readings[0].ReplacedByID != "corrected-h" {
		t.Fatalf("登记读数勘误失败: %#v %v", in, err)
	}
	replayed, err := svc.CorrectRegistrationReading(in.ID, 1, "operations-h", replacement, "原值录入错误", "保管员", "correct-reading")
	if err != nil || replayed.Revision != in.Revision {
		t.Fatalf("登记勘误幂等重放失败: %#v %v", replayed, err)
	}
	items := []domain.MitigationItem{{ID: "first", Description: "隔离"}, {ID: "second", Description: "调节", PrerequisiteIDs: []string{"first"}}}
	in, err = svc.Assign(in.ID, in.Revision, "执行人甲", now.Add(time.Hour), "处置", items, "负责人", "operations-assign")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.TransferAssignee(in.ID, in.Revision, "执行人乙", "原执行人请假", now.Add(90*time.Minute), "负责人", "operations-transfer")
	if err != nil || in.Assignee != "执行人乙" || len(in.AssigneeTransfers) != 1 || in.Plan.Progress != 0 {
		t.Fatalf("执行人交接失败: %#v %v", in, err)
	}
	bad := []domain.ItemCompletion{{ItemID: "second", Note: "先调节", EffectReadings: []domain.EnvironmentalReading{effect("bad-effect", now, 55, "bad-effect-evidence")}}}
	_, err = svc.RecordItemsBatch(in.ID, in.Revision, bad, "执行人乙", "bad-batch")
	if err == nil {
		t.Fatal("依赖未完成的批次应失败")
	}
	unchanged, _ := repo.Get(in.ID)
	if unchanged.Revision != in.Revision || unchanged.Plan.Progress != 0 || len(unchanged.Timeline) != len(in.Timeline) {
		t.Fatal("失败批次产生了部分写入")
	}
	good := []domain.ItemCompletion{
		{ItemID: "first", Note: "完成隔离", EffectReadings: []domain.EnvironmentalReading{effect("first-effect", now, 60, "first-effect-evidence")}},
		{ItemID: "second", Note: "完成调节", EffectReadings: []domain.EnvironmentalReading{effect("second-effect", now, 55, "second-effect-evidence")}},
	}
	in, err = svc.RecordItemsBatch(in.ID, in.Revision, good, "执行人乙", "good-batch")
	if err != nil || in.Revision != unchanged.Revision+1 || in.Plan.Progress != 1 || in.Timeline[len(in.Timeline)-1].EventType != "措施批量完成" {
		t.Fatalf("批量原子完成失败: %#v %v", in, err)
	}
}

func TestEffectReboundBlocksReviewAndArchiveViewIsReadOnly(t *testing.T) {
	clock := time.Now().UTC()
	repo := domain.NewMemoryRepo()
	svc := &Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return clock }}
	in := assignOne(t, svc, clock, createHumidityIncident(t, svc, clock, "stability", 72), "stability-assign")
	clock = clock.Add(3 * time.Hour)
	readings := []domain.EnvironmentalReading{effect("stable-first", clock.Add(-2*time.Hour), 55, "stable-first-evidence"), effect("rebound", clock, 70, "rebound-evidence")}
	in, err := svc.RecordReadings(in.ID, in.Revision, "item-1", "连续复测", readings, "执行人", "stability-item")
	if err != nil || len(in.RetestMetrics) != 1 || !in.Stability[0].Rebounded {
		t.Fatalf("反弹趋势未记录: %#v %v", in.Stability, err)
	}
	_, err = svc.Submit(in.ID, in.Revision, "执行人", "stability-submit")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || len(validation.MissingMetrics) != 1 {
		t.Fatalf("不稳定复测未阻止复核: %v", err)
	}
	if _, err = svc.GetArchive(in.ID); !errors.Is(err, domain.ErrState) {
		t.Fatalf("非关闭事件不应返回归档: %v", err)
	}
	stored, _ := repo.Get(in.ID)
	if stored.Revision != in.Revision {
		t.Fatal("失败提交或归档查询改变了修订号")
	}
}
