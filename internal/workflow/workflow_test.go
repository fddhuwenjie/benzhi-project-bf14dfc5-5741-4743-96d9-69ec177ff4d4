package workflow

import (
	"errors"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"testing"
	"time"
)

func newTestService(now time.Time) (*Service, *domain.MemoryRepo) {
	repo := domain.NewMemoryRepo()
	svc := &Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	return svc, repo
}

func createHumidityIncident(t *testing.T, svc *Service, now time.Time, id string, value float64) *domain.PreservationIncident {
	t.Helper()
	reading := domain.EnvironmentalReading{ID: id + "-h", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: value, Unit: "%RH", MeasuredAt: now, SourceNote: "湿度记录仪", EvidenceRef: id + "-registration-evidence", EvidenceRecordedAt: now}
	in, err := svc.Create(CreateCommand{ID: id, AreaID: "库房甲", AffectedScope: "纸本文物", Sensitivity: "高", Actor: "保管员", RequestID: id + "-create", ObservedAt: now, SubmittedAt: now, Readings: []domain.EnvironmentalReading{reading}})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func assignOne(t *testing.T, svc *Service, now time.Time, in *domain.PreservationIncident, requestID string) *domain.PreservationIncident {
	t.Helper()
	assigned, err := svc.Assign(in.ID, in.Revision, "执行人", now.Add(time.Hour), "降低湿度", []domain.MitigationItem{{ID: "item-1", Description: "启动除湿设备"}}, "负责人", requestID)
	if err != nil {
		t.Fatal(err)
	}
	return assigned
}

func effect(id string, now time.Time, value float64, evidence string) domain.EnvironmentalReading {
	return domain.EnvironmentalReading{ID: id, Metric: "湿度", Value: value, Unit: "%", MeasuredAt: now, SourceNote: "复测仪表", EvidenceRef: evidence, EvidenceRecordedAt: now}
}

func TestCreateValidationDoesNotPersistAnything(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	bad := domain.EnvironmentalReading{ID: "r", Phase: domain.PhaseAbnormal, Metric: "温度", Value: 35, Unit: "℃", MeasuredAt: now.Add(time.Minute), SourceNote: "仪表", EvidenceRef: "ev", EvidenceRecordedAt: now.Add(time.Minute)}
	_, err := svc.Create(CreateCommand{ID: "bad", AreaID: "A", AffectedScope: "纸本", Sensitivity: "高", Actor: "保管员", RequestID: "bad-request", ObservedAt: now, SubmittedAt: now, Readings: []domain.EnvironmentalReading{bad}})
	if err == nil {
		t.Fatal("无效测量时间应登记失败")
	}
	if len(repo.List(domain.IncidentFilter{})) != 0 {
		t.Fatal("失败登记不应生成事件")
	}
	if _, ok := repo.FindRequest("bad-request"); ok {
		t.Fatal("失败登记不应生成幂等成功记录")
	}
}

func TestItemIdempotencyAndCrossOperationConflict(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	in := assignOne(t, svc, now, createHumidityIncident(t, svc, now, "idem", 70), "assign-request")
	reading := effect("effect-1", now, 55, "effect-evidence-1")
	first, err := svc.RecordReadings(in.ID, in.Revision, "item-1", "完成除湿", []domain.EnvironmentalReading{reading}, "执行人", "stable-request")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.RecordReadings(in.ID, in.Revision, "item-1", "完成除湿", []domain.EnvironmentalReading{reading}, "执行人", "stable-request")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != second.Revision || second.Revision != 3 || len(second.Timeline) != 3 {
		t.Fatalf("重复完成改变了结果: rev=%d timeline=%d", second.Revision, len(second.Timeline))
	}
	_, err = svc.Submit(in.ID, second.Revision, "执行人", "stable-request")
	if !errors.Is(err, domain.ErrIdempotency) {
		t.Fatalf("跨操作复用应冲突: %v", err)
	}
	stored, _ := repo.Get(in.ID)
	if stored.Status != domain.StatusMitigating || stored.Revision != 3 || len(stored.Timeline) != 3 {
		t.Fatalf("幂等冲突改变了聚合: %#v", stored)
	}
}

func TestAssignmentRiskDeadlineAndOverdueExplanation(t *testing.T) {
	now := time.Now().UTC()
	svc, repo := newTestService(now)
	in := createHumidityIncident(t, svc, now, "deadline", 70)
	items := []domain.MitigationItem{{ID: "item-1", Description: "启动除湿设备"}}
	_, err := svc.Assign(in.ID, in.Revision, "执行人", in.Deadline.LatestResponseAt.Add(time.Minute), "除湿", items, "负责人", "deadline-too-late")
	if err == nil {
		t.Fatal("未逾期事件不应接受晚于最晚响应时间的期限")
	}
	unchanged, _ := repo.Get(in.ID)
	if unchanged.Revision != 1 || unchanged.Status != domain.StatusPending {
		t.Fatal("期限校验失败不应修改聚合")
	}

	oldObserved := now.Add(-48 * time.Hour)
	oldReading := domain.EnvironmentalReading{ID: "overdue-h", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: oldObserved, SourceNote: "湿度记录仪", EvidenceRef: "overdue-evidence", EvidenceRecordedAt: oldObserved}
	overdue, err := svc.Create(CreateCommand{ID: "overdue", AreaID: "库房甲", AffectedScope: "纸本文物", Sensitivity: "高", Actor: "保管员", RequestID: "overdue-create", ObservedAt: oldObserved, SubmittedAt: now, Readings: []domain.EnvironmentalReading{oldReading}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Assign(overdue.ID, overdue.Revision, "执行人", now.Add(time.Hour), "除湿", items, "负责人", "overdue-no-note")
	if err == nil {
		t.Fatal("逾期事件缺少说明时不应允许分派")
	}
	assigned, err := svc.AssignWithOverdue(overdue.ID, overdue.Revision, "执行人", now.Add(time.Hour), "除湿", items, "负责人", "overdue-with-note", "设备故障导致未及时响应")
	if err != nil || assigned.Status != domain.StatusMitigating {
		t.Fatalf("带说明的逾期分派失败: %#v %v", assigned, err)
	}
	if assigned.Plan.OverdueNote == "" {
		t.Fatal("分派事件未保存逾期说明")
	}
}

func TestVerificationRejectsExceededReadingAndSupportsSecondRound(t *testing.T) {
	now := time.Now().UTC()
	svc, _ := newTestService(now)
	in := assignOne(t, svc, now, createHumidityIncident(t, svc, now, "rounds", 72), "rounds-assign")
	in, err := svc.RecordReadings(in.ID, in.Revision, "item-1", "首轮除湿", []domain.EnvironmentalReading{effect("effect-high", now, 68, "round-1-evidence")}, "执行人", "rounds-item-1")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.Submit(in.ID, in.Revision, "执行人", "rounds-submit-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Verify(in.ID, in.Revision, "复核人", "合格", "", "rounds-pass-invalid"); err == nil {
		t.Fatal("仍超阈时不应允许合格关闭")
	}
	unchanged, _ := svc.Get(in.ID)
	if unchanged.Revision != in.Revision || unchanged.Status != domain.StatusReview {
		t.Fatal("失败复核不应改变事件")
	}
	in, err = svc.Verify(in.ID, in.Revision, "复核人", "退回", "湿度仍高，继续除湿", "rounds-return")
	if err != nil {
		t.Fatal(err)
	}
	if in.CurrentRound != 2 || len(in.Rounds) != 2 || in.Rounds[0].FrozenAt == nil {
		t.Fatalf("退回轮次不完整: %#v", in.Rounds)
	}
	in, err = svc.RecordReadings(in.ID, in.Revision, "round-2-item-1", "二轮继续除湿", []domain.EnvironmentalReading{effect("effect-normal", now, 55, "round-2-evidence")}, "执行人", "rounds-item-2")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.Submit(in.ID, in.Revision, "执行人", "rounds-submit-2")
	if err != nil {
		t.Fatal(err)
	}
	in, err = svc.Verify(in.ID, in.Revision, "复核人", "合格", "读数恢复", "rounds-pass-2")
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != domain.StatusClosed || in.ClosedRound != 2 || len(in.Rounds) != 2 || in.Rounds[1].Verification == nil {
		t.Fatalf("最终关闭未关联第二轮: status=%s round=%d", in.Status, in.ClosedRound)
	}
}
