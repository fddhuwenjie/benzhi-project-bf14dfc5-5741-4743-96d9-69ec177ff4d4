package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type Status string

const (
	StatusPending    Status = "待分派"
	StatusMitigating Status = "处置中"
	StatusReview     Status = "待复核"
	StatusClosed     Status = "已关闭"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "低"
	RiskMedium   RiskLevel = "中"
	RiskHigh     RiskLevel = "高"
	RiskCritical RiskLevel = "紧急"
)

type ReadingPhase string

const (
	PhaseBaseline ReadingPhase = "baseline"
	PhaseAbnormal ReadingPhase = "abnormal"
	PhaseEffect   ReadingPhase = "effect"
)

type HandoverSignature struct {
	SnapshotID string    `json:"snapshot_id"`
	Checksum   string    `json:"checksum"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Shift      string    `json:"shift"`
	SignedAt   time.Time `json:"signed_at"`
}

var ErrConflict = errors.New("revision conflict")
var ErrNotFound = errors.New("not found")
var ErrInvalid = errors.New("invalid request")
var ErrState = errors.New("invalid state")
var ErrIdempotency = errors.New("idempotency conflict")
var ErrIntegrity = errors.New("data integrity error")

type ValidationError struct {
	Field          string
	Message        string
	MissingMetrics []string
	Comparisons    []ReadingComparison
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

type FieldIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type CandidateConflictError struct {
	Kind       string              `json:"kind"`
	Candidates []IncidentCandidate `json:"candidates"`
	Message    string              `json:"message"`
}

func (e *CandidateConflictError) Error() string { return e.Message }
func (e *CandidateConflictError) Unwrap() error { return ErrConflict }

type WorkloadConflictError struct {
	Snapshot WorkloadSnapshot `json:"workload_snapshot"`
	Message  string           `json:"message"`
}

func (e *WorkloadConflictError) Error() string { return e.Message }
func (e *WorkloadConflictError) Unwrap() error { return ErrConflict }

type DependencyBlockedError struct {
	ItemID    string   `json:"item_id"`
	BlockedBy []string `json:"blocked_by"`
}

func (e *DependencyBlockedError) Error() string { return "措施项的前置项尚未完成" }
func (e *DependencyBlockedError) Unwrap() error { return ErrState }

type IntegrityError struct{ Message string }

func (e *IntegrityError) Error() string { return e.Message }
func (e *IntegrityError) Unwrap() error { return ErrIntegrity }

type IdempotencyConflictError struct {
	IncidentID string
	Status     Status
	Revision   int
}

func (e *IdempotencyConflictError) Error() string { return ErrIdempotency.Error() }
func (e *IdempotencyConflictError) Unwrap() error { return ErrIdempotency }

// RecordedFailureError wraps a serialised domain error produced by a
// previously recorded failed request (for example a rejected manual review).
// It lets the idempotency layer replay the same rejection on retries without
// creating new audit events or changing the revision, while preserving
// errors.As/errors.Is compatibility with the original error type.
type RecordedFailureError struct {
	OriginalError string
}

func (e *RecordedFailureError) Error() string { return e.OriginalError }
func (e *RecordedFailureError) Unwrap() error { return ErrInvalid }

// EncodeValidationError serialises a validation error so it can be stored in
// a request record and later replayed by DecodeRecordedError.
func EncodeValidationError(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		b, _ := json.Marshal(struct {
			Type     string   `json:"type"`
			Field    string   `json:"field"`
			Message  string   `json:"message"`
			Missing  []string `json:"missing,omitempty"`
		}{"validation", ve.Field, ve.Message, ve.MissingMetrics})
		return string(b)
	}
	return err.Error()
}

// DecodeRecordedError rebuilds the most specific domain error from a stored
// serialisation produced by EncodeValidationError.
func DecodeRecordedError(stored string) error {
	var payload struct {
		Type    string   `json:"type"`
		Field   string   `json:"field"`
		Message string   `json:"message"`
		Missing []string  `json:"missing"`
	}
	if json.Unmarshal([]byte(stored), &payload) == nil && payload.Type == "validation" {
		return &ValidationError{Field: payload.Field, Message: payload.Message, MissingMetrics: payload.Missing}
	}
	return &RecordedFailureError{OriginalError: stored}
}

type IncidentFilter struct {
	Status          Status    `json:"status,omitempty"`
	AreaID          string    `json:"area_id,omitempty"`
	RiskLevel       RiskLevel `json:"risk_level,omitempty"`
	DeadlineBucket  string    `json:"deadline_bucket,omitempty"`
	ObservedFrom    time.Time `json:"observed_from,omitempty"`
	ObservedTo      time.Time `json:"observed_to,omitempty"`
	CollectionID    string    `json:"collection_id,omitempty"`
	Material        string    `json:"material,omitempty"`
	ItemSensitivity string    `json:"sensitivity,omitempty"`
}

type RequestRecord struct {
	RequestID       string                `json:"request_id"`
	Operation       string                `json:"operation"`
	IncidentID      string                `json:"incident_id"`
	Digest          string                `json:"digest"`
	SuccessRevision int                   `json:"success_revision"`
	Result          *PreservationIncident `json:"result,omitempty"`
	Failure         bool                  `json:"failure,omitempty"`
	FailureError    string                `json:"failure_error,omitempty"`
}

type Repository interface {
	Save(*PreservationIncident, int) error
	Commit(*PreservationIncident, int, RequestRecord) error
	CommitFailure(*PreservationIncident, int, RequestRecord) error
	Get(string) (*PreservationIncident, error)
	List(IncidentFilter) []*PreservationIncident
	FindRequest(string) (RequestRecord, bool)
	RecordRequest(string, string) (string, bool)
	AllEvents(string) []IncidentEvent
	CommitBatch([]*PreservationIncident, map[string]int, BatchRequestRecord) error
	FindBatchRequest(string) (BatchRequestRecord, bool)
}

func NewIncident(id, area, scope, sensitivity string, observed time.Time, readings []EnvironmentalReading, risk RiskLevel, basis []string, due time.Duration) (*PreservationIncident, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(area) == "" || strings.TrimSpace(scope) == "" || observed.IsZero() || len(readings) == 0 {
		return nil, &ValidationError{Field: "incident", Message: "事件编号、区域、藏品范围、观测时间和读数均为必填项"}
	}
	now := time.Now().UTC()
	in := &PreservationIncident{ID: id, AreaID: area, AffectedScope: scope, ObservedAt: observed, Status: StatusPending, RiskLevel: risk, RiskBasis: append([]string(nil), basis...), ResponseDue: due, Revision: 1, CreatedAt: now, UpdatedAt: now, Sensitivity: sensitivity, Readings: append([]EnvironmentalReading(nil), readings...)}
	in.RefreshDeadline(now)
	return in, nil
}

func (i *PreservationIncident) SetRegistrationDetails(intervals []AbnormalInterval) {
	i.AssessmentIntervals = append([]AbnormalInterval(nil), intervals...)
	refs := make([]string, 0, len(i.Readings))
	metrics := map[string]bool{}
	for _, r := range i.Readings {
		if r.EvidenceRef == "" {
			continue
		}
		i.Evidence = append(i.Evidence, EvidenceSummary{ReadingID: r.ID, Metric: r.Metric, Reference: r.EvidenceRef, SourceNote: r.SourceNote, RecordedAt: r.EvidenceRecordedAt})
		refs = append(refs, r.EvidenceRef)
		metrics[r.Metric] = true
	}
	metricNames := make([]string, 0, len(metrics))
	for m := range metrics {
		metricNames = append(metricNames, m)
	}
	sort.Strings(metricNames)
	i.appendEvent("登记与研判", "保管员", "", map[string]interface{}{"risk": i.RiskLevel, "risk_basis": i.RiskBasis, "evidence_count": len(i.Evidence), "evidence_refs": refs, "metrics": metricNames, "continuous_intervals": intervals})
}

func (i *PreservationIncident) RefreshDeadline(now time.Time) {
	latest := i.ObservedAt.Add(i.ResponseDue)
	d := latest.Sub(now)
	i.Deadline = DeadlineInfo{LatestResponseAt: latest, Remaining: d, Overdue: d < 0, AvailableFrom: now, AvailableTo: latest}
	if d < 0 {
		i.Deadline.OverdueDuration = -d
		i.Deadline.Remaining = 0
		i.Deadline.AvailableTo = now.Add(i.ResponseDue)
	}
}

func (i *PreservationIncident) apply(expected int, transition Status) error {
	return i.applyAt(expected, transition, time.Now().UTC())
}

func (i *PreservationIncident) applyAt(expected int, transition Status, now time.Time) error {
	if i.Revision != expected {
		return ErrConflict
	}
	i.Revision++
	i.Status = transition
	i.UpdatedAt = now
	return nil
}

func (i *PreservationIncident) AssignWithDeadline(expected int, assignee string, due time.Time, plan MitigationPlan, actor, req, overdueNote string, now time.Time) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.PendingManualReview {
		return &ValidationError{Field: "manual_review", Message: "读数可信度待人工复核，确认后才能分派"}
	}
	if strings.TrimSpace(assignee) == "" || due.IsZero() || len(plan.Items) == 0 {
		return &ValidationError{Field: "assignment", Message: "执行人、期限和措施项均为必填项"}
	}
	if due.Before(now) {
		return &ValidationError{Field: "due_at", Message: "分派期限不得早于当前时间"}
	}
	latest := i.ObservedAt.Add(i.ResponseDue)
	overdue := now.After(latest)
	if !overdue && due.After(latest) {
		return &ValidationError{Field: "due_at", Message: fmt.Sprintf("分派期限不得晚于 %s", latest.Format(time.RFC3339))}
	}
	if overdue && strings.TrimSpace(overdueNote) == "" {
		return &ValidationError{Field: "overdue_note", Message: "事件已超过建议响应时限，必须填写逾期说明"}
	}
	if overdue && due.After(now.Add(i.ResponseDue)) {
		return &ValidationError{Field: "due_at", Message: fmt.Sprintf("逾期分派期限不得晚于 %s", now.Add(i.ResponseDue).Format(time.RFC3339))}
	}
	if utf8.RuneCountInString(strings.TrimSpace(overdueNote)) > 500 {
		return &ValidationError{Field: "overdue_note", Message: "逾期说明不得超过 500 个字符"}
	}
	for n := range plan.Items {
		if strings.TrimSpace(plan.Items[n].ID) == "" {
			plan.Items[n].ID = fmt.Sprintf("round-1-item-%d", n+1)
		}
		if strings.TrimSpace(plan.Items[n].Description) == "" {
			return &ValidationError{Field: fmt.Sprintf("items[%d].description", n), Message: "措施说明不能为空"}
		}
		plan.Items[n].Status = "待执行"
		plan.Items[n].Note = ""
		plan.Items[n].EffectReadingIDs = nil
		plan.Items[n].Evidence = ""
		plan.Items[n].CompletedAt = nil
	}
	if err := validateDependencies(plan.Items); err != nil {
		return err
	}
	refreshPlanProgress(&plan)
	if err := i.apply(expected, StatusMitigating); err != nil {
		return err
	}
	i.Assignee, i.DueAt = assignee, due
	plan.IncidentID, plan.ID, plan.Round = i.ID, i.ID+"-plan-1", 1
	plan.Owner, plan.DueAt, plan.OverdueNote = assignee, due, strings.TrimSpace(overdueNote)
	plan.SubmittedAt = ptrTime(now)
	i.Plan, i.CurrentRound = &plan, 1
	i.Rounds = append(i.Rounds, TreatmentRound{Number: 1, Plan: clonePlan(plan), StartedAt: now})
	i.appendEvent("分派", actor, req, map[string]interface{}{"assignee": assignee, "plan_id": plan.ID, "due_at": due, "latest_response_at": latest, "remaining_response": latest.Sub(now).String(), "overdue": overdue, "overdue_note": plan.OverdueNote, "workload_snapshot": plan.Workload})
	if overdue {
		i.appendEvent("逾期升级", actor, req, map[string]interface{}{"level": "已逾期", "previous_due_at": latest, "reason": strings.TrimSpace(overdueNote)})
	}
	i.syncCurrentRound()
	return nil
}

// Assign 保留原有领域入口，公开工作流使用带逾期说明的 AssignWithDeadline。
func (i *PreservationIncident) Assign(expected int, assignee string, due time.Time, plan MitigationPlan, actor, req string) error {
	return i.AssignWithDeadline(expected, assignee, due, plan, actor, req, "", time.Now().UTC())
}

func (i *PreservationIncident) RecordItemReadings(expected int, itemID, note string, effects []EnvironmentalReading, actor, req string, now time.Time) error {
	return i.recordItemReadings(expected, itemID, note, effects, nil, actor, req, now)
}

func (i *PreservationIncident) CompleteItemReadings(expected int, itemID, note string, effects []EnvironmentalReading, processRecordSequences []int, actor, req string, now time.Time) error {
	return i.recordItemReadings(expected, itemID, note, effects, processRecordSequences, actor, req, now)
}

func (i *PreservationIncident) recordItemReadings(expected int, itemID, note string, effects []EnvironmentalReading, processRecordSequences []int, actor, req string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > 2000 {
		return &ValidationError{Field: "note", Message: "措施说明不能为空且不得超过 2000 个字符"}
	}
	if len(effects) == 0 {
		return &ValidationError{Field: "effect_readings", Message: "至少需要一条结构化效果读数"}
	}
	idx := -1
	for n := range i.Plan.Items {
		if i.Plan.Items[n].ID == itemID {
			idx = n
			break
		}
	}
	if idx < 0 {
		return &ValidationError{Field: "item_id", Message: "当前处置轮次不存在该措施项"}
	}
	if i.Plan.Items[idx].Status == "已完成" || i.Plan.Items[idx].Status == "已取消" {
		return &ValidationError{Field: "item_id", Message: "措施项已完成或已取消，不能修改"}
	}
	if i.Plan.Items[idx].PausedAt != nil {
		return ErrState
	}
	if len(i.Plan.Items[idx].ProcessRecords) > 0 || len(processRecordSequences) > 0 {
		if err := i.ValidateCompletionRecords(itemID, processRecordSequences); err != nil {
			return err
		}
	}
	blocked := incompletePrerequisites(i.Plan.Items, i.Plan.Items[idx])
	if len(blocked) > 0 {
		return &DependencyBlockedError{ItemID: itemID, BlockedBy: blocked}
	}
	assignmentAt := i.Rounds[len(i.Rounds)-1].StartedAt
	ids := make([]string, 0, len(effects))
	seenIDs := map[string]bool{}
	seenEvidence := map[string]bool{}
	for _, existing := range i.Readings {
		seenIDs[existing.ID] = true
		seenEvidence[strings.TrimSpace(existing.EvidenceRef)] = true
	}
	for n := range effects {
		r := &effects[n]
		if r.ID == "" {
			r.ID = fmt.Sprintf("%s-r%d-%s-effect-%d", i.ID, i.CurrentRound, itemID, n+1)
		}
		if seenIDs[r.ID] {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].id", n), Message: "效果读数标识重复"}
		}
		seenIDs[r.ID] = true
		if r.MeasuredAt.Before(assignmentAt) {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].measured_at", n), Message: "效果测量时间不得早于本轮分派时间"}
		}
		if r.MeasuredAt.After(now) {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].measured_at", n), Message: "效果测量时间不得晚于提交时间"}
		}
		if strings.TrimSpace(r.SourceNote) == "" || utf8.RuneCountInString(r.SourceNote) > 500 {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].source_note", n), Message: "来源说明不能为空且不得超过 500 个字符"}
		}
		ref := strings.TrimSpace(r.EvidenceRef)
		if ref == "" || utf8.RuneCountInString(ref) > 500 {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].evidence_ref", n), Message: "证据引用不能为空且不得超过 500 个字符"}
		}
		if seenEvidence[ref] {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].evidence_ref", n), Message: "证据引用不得复用于其他读数"}
		}
		seenEvidence[ref] = true
		if r.EvidenceRecordedAt.Before(r.MeasuredAt) || r.EvidenceRecordedAt.After(now) {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].evidence_recorded_at", n), Message: "证据时间必须介于效果测量与提交之间"}
		}
		r.IncidentID, r.Phase = i.ID, PhaseEffect
		r.SourceNote, r.EvidenceRef = strings.TrimSpace(r.SourceNote), ref
		ids = append(ids, r.ID)
	}
	i.Plan.Items[idx].Status, i.Plan.Items[idx].Note = "已完成", note
	i.Plan.Items[idx].EffectReadingIDs, i.Plan.Items[idx].Evidence = ids, effects[0].EvidenceRef
	i.Plan.Items[idx].CompletedAt = ptrTime(now)
	i.Readings = append(i.Readings, effects...)
	refreshPlanProgress(i.Plan)
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("措施完成", actor, req, map[string]interface{}{"item_id": itemID, "effect_reading_ids": ids, "metrics": uniqueMetrics(effects)})
	i.syncCurrentRound()
	return nil
}

// PauseItem 暂停当前轮次中可执行的未完成措施，保留进度并阻止完成记录。
func (i *PreservationIncident) PauseItem(expected int, itemID, reason string, startedAt, resumeAt time.Time, actor, req string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" {
		return &ValidationError{Field: "pause_reason", Message: "暂停原因不能为空"}
	}
	if startedAt.IsZero() {
		startedAt = now
	}
	if resumeAt.IsZero() || resumeAt.Before(startedAt) {
		return &ValidationError{Field: "expected_resume_at", Message: "预计恢复时间必须晚于开始时间"}
	}
	for n := range i.Plan.Items {
		item := &i.Plan.Items[n]
		if item.ID != itemID {
			continue
		}
		if item.Status == "已完成" {
			return &ValidationError{Field: "item_id", Message: "已完成措施不能暂停"}
		}
		if len(item.BlockedBy) > 0 || !item.Executable {
			return &DependencyBlockedError{ItemID: itemID, BlockedBy: item.BlockedBy}
		}
		item.PausedAt = ptrTime(startedAt)
		item.PauseReason = strings.TrimSpace(reason)
		item.ExpectedResumeAt = ptrTime(resumeAt)
		i.Revision++
		i.UpdatedAt = now
		i.appendEvent("措施暂停", actor, req, map[string]interface{}{"item_id": itemID, "reason": item.PauseReason, "expected_resume_at": resumeAt})
		refreshPlanProgress(i.Plan)
		i.syncCurrentRound()
		return nil
	}
	return &ValidationError{Field: "item_id", Message: "当前处置轮次不存在该措施项"}
}

func (i *PreservationIncident) ResumeItem(expected int, itemID string, resumedAt time.Time, actor, req string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if resumedAt.IsZero() || resumedAt.After(now) {
		return &ValidationError{Field: "resumed_at", Message: "恢复时间必须为有效过去时间"}
	}
	for n := range i.Plan.Items {
		item := &i.Plan.Items[n]
		if item.ID != itemID {
			continue
		}
		if item.PausedAt == nil {
			return &ValidationError{Field: "item_id", Message: "措施项当前未暂停"}
		}
		item.PausedAt, item.PauseReason, item.ExpectedResumeAt = nil, "", nil
		i.Revision++
		i.UpdatedAt = now
		i.appendEvent("措施恢复", actor, req, map[string]interface{}{"item_id": itemID, "resumed_at": resumedAt})
		refreshPlanProgress(i.Plan)
		i.syncCurrentRound()
		return nil
	}
	return &ValidationError{Field: "item_id", Message: "当前处置轮次不存在该措施项"}
}

// RecordItem 保留旧的领域调用入口；公开 HTTP 工作流只接受结构化效果读数。
func (i *PreservationIncident) RecordItem(expected int, itemID, note, effect, evidence, actor, req string) error {
	now := time.Now().UTC()
	v := 0.0
	fmt.Sscanf(effect, "%f", &v)
	metric, unit := "温度", "℃"
	for _, r := range i.Readings {
		if r.Phase == PhaseAbnormal {
			metric, unit = r.Metric, r.Unit
			break
		}
	}
	r := EnvironmentalReading{Metric: metric, Value: v, Unit: unit, OriginalValue: v, OriginalUnit: unit, MeasuredAt: now, SourceNote: note, EvidenceRef: evidence, EvidenceRecordedAt: now}
	return i.RecordItemReadings(expected, itemID, note, []EnvironmentalReading{r}, actor, req, now)
}

func (i *PreservationIncident) SubmitReviewWithComparisons(expected int, actor, req string, comparisons []ReadingComparison) error {
	return i.SubmitReviewWithComparisonsAt(expected, actor, req, comparisons, time.Now().UTC())
}

func (i *PreservationIncident) SubmitReviewWithComparisonsAt(expected int, actor, req string, comparisons []ReadingComparison, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if missing := i.RetestGate(now); len(missing) > 0 {
		return &ValidationError{Field: "retest_checkpoints", Message: "存在未满足的复测检查点", MissingMetrics: missing, Comparisons: comparisons}
	}
	if len(i.RetestMetrics) > 0 {
		return &ValidationError{Field: "effect_readings", Message: "效果复测尚不稳定，需补充复测后再提交复核", MissingMetrics: append([]string(nil), i.RetestMetrics...), Comparisons: comparisons}
	}
	for n, it := range i.Plan.Items {
		if it.Status == "已取消" {
			continue
		}
		if it.Status != "已完成" {
			return &ValidationError{Field: fmt.Sprintf("items[%d]", n), Message: "措施项尚未完成"}
		}
	}
	missing := missingComparisonMetrics(comparisons)
	if len(missing) > 0 {
		return &ValidationError{Field: "effect_readings", Message: "异常指标缺少可比较的效果读数", MissingMetrics: missing, Comparisons: comparisons}
	}
	if err := i.apply(expected, StatusReview); err != nil {
		return err
	}
	i.Plan.CompletedAt = &now
	i.Comparisons = append([]ReadingComparison(nil), comparisons...)
	i.appendEvent("提交复核", actor, req, map[string]interface{}{"comparisons": comparisons})
	i.syncCurrentRound()
	return nil
}

// SubmitReview 保留原有领域入口，公开工作流会传入服务端生成的对比摘要。
func (i *PreservationIncident) SubmitReview(expected int, actor, req string) error {
	return i.SubmitReviewWithComparisons(expected, actor, req, i.Comparisons)
}

func (i *PreservationIncident) VerifyWithComparisons(expected int, reviewer, decision, reason, req string, comparisons []ReadingComparison) error {
	return i.VerifyWithComparisonsAt(expected, reviewer, decision, reason, req, comparisons, time.Now().UTC())
}

// VerifyWithCategory 是带结构化退回分类的复核入口；旧入口继续兼容。
func (i *PreservationIncident) VerifyWithCategory(expected int, reviewer, decision, category, reason, req string, comparisons []ReadingComparison, confirmedIDs []string, now time.Time) error {
	if decision == "退回" {
		allowed := map[string]bool{"读数未稳定": true, "证据不足": true, "措施未完成": true, "其他": true}
		if !allowed[strings.TrimSpace(category)] {
			return &ValidationError{Field: "return_category", Message: "退回分类只能为读数未稳定、证据不足、措施未完成或其他"}
		}
		if strings.TrimSpace(reason) == "" {
			return &ValidationError{Field: "reason", Message: "退回补充说明不能为空"}
		}
	}
	return i.verifyWithCategory(expected, reviewer, decision, category, reason, req, comparisons, confirmedIDs, now)
}

func (i *PreservationIncident) verifyWithCategory(expected int, reviewer, decision, category, reason, req string, comparisons []ReadingComparison, confirmedIDs []string, now time.Time) error {
	// 复用原状态机；分类通过事件载荷补充，避免破坏旧调用。
	if err := i.VerifyConfirmedWithComparisonsAt(expected, reviewer, decision, reason, req, comparisons, confirmedIDs, now); err != nil {
		return err
	}
	if decision == "退回" {
		if i.Verification != nil {
			i.Verification.ReturnCategory = category
		}
	}
	if decision == "退回" && len(i.Timeline) > 0 {
		i.Timeline[len(i.Timeline)-1].Payload["return_category"] = category
	}
	return nil
}

func returnCategory(reason string) string {
	r := strings.TrimSpace(reason)
	if strings.Contains(r, "稳定") {
		return "读数未稳定"
	}
	if strings.Contains(r, "证据") {
		return "证据不足"
	}
	if strings.Contains(r, "措施") {
		return "措施未完成"
	}
	return "其他"
}

func (i *PreservationIncident) VerifyWithComparisonsAt(expected int, reviewer, decision, reason, req string, comparisons []ReadingComparison, now time.Time) error {
	return i.VerifyConfirmedWithComparisonsAt(expected, reviewer, decision, reason, req, comparisons, comparisonIDs(comparisons), now)
}

func (i *PreservationIncident) VerifyConfirmedWithComparisonsAt(expected int, reviewer, decision, reason, req string, comparisons []ReadingComparison, confirmedIDs []string, now time.Time) error {
	if i.Status != StatusReview {
		return ErrState
	}
	if decision != "合格" && decision != "退回" {
		return &ValidationError{Field: "decision", Message: "决定只能为合格或退回"}
	}
	if strings.TrimSpace(reviewer) == "" {
		return &ValidationError{Field: "reviewer", Message: "复核人不能为空"}
	}
	responsibility, err := i.checkVerificationResponsibility(strings.TrimSpace(reviewer), now)
	if err != nil {
		return err
	}
	if err = i.validateReviewEvidence(comparisons, confirmedIDs); err != nil {
		return err
	}
	if decision == "退回" && (strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 1000) {
		return &ValidationError{Field: "reason", Message: "退回原因不能为空且不得超过 1000 个字符"}
	}
	if decision == "合格" {
		missing := missingComparisonMetrics(comparisons)
		for _, c := range comparisons {
			if !c.WithinThreshold {
				missing = append(missing, c.Metric+"仍超出阈值")
			}
		}
		if len(missing) > 0 {
			return &ValidationError{Field: "decision", Message: "存在缺失或仍超阈的效果读数，不能合格关闭", MissingMetrics: missing, Comparisons: comparisons}
		}
	}
	target := StatusMitigating
	if decision == "合格" {
		target = StatusClosed
	}
	if err := i.applyAt(expected, target, now); err != nil {
		return err
	}
	ids := comparisonIDs(comparisons)
	v := &Verification{ID: fmt.Sprintf("%s-verification-%d", i.ID, i.CurrentRound), IncidentID: i.ID, Reviewer: reviewer, Decision: decision, Reason: strings.TrimSpace(reason), ComparedReadingIDs: ids, Comparisons: append([]ReadingComparison(nil), comparisons...), VerifiedAt: now, Round: i.CurrentRound, ConfirmedReadingIDs: append([]string(nil), confirmedIDs...), ResponsibilityCheck: responsibility}
	i.Verification = v
	i.Comparisons = append([]ReadingComparison(nil), comparisons...)
	i.syncCurrentRound()
	i.Rounds[len(i.Rounds)-1].Verification = cloneVerification(v)
	i.Rounds[len(i.Rounds)-1].Comparisons = append([]ReadingComparison(nil), comparisons...)
	if decision == "合格" {
		i.ClosedRound = i.CurrentRound
		i.Rounds[len(i.Rounds)-1].FrozenAt = &now
		i.appendEvent("关闭", reviewer, req, map[string]interface{}{"round": i.CurrentRound, "verification_id": v.ID, "compared_reading_ids": ids, "confirmed_reading_ids": confirmedIDs, "responsibility_check": responsibility})
		return nil
	}
	i.Rounds[len(i.Rounds)-1].FrozenAt = &now
	i.Rounds[len(i.Rounds)-1].ReturnedReason = strings.TrimSpace(reason)
	previousRound := i.CurrentRound
	i.CurrentRound++
	itemID := fmt.Sprintf("round-%d-item-1", i.CurrentRound)
	// 退回后复用上一轮未完成或不稳定措施，已完成项仅保留在历史轮次。
	var items []MitigationItem
	for _, old := range i.Rounds[len(i.Rounds)-1].Plan.Items {
		if old.Status == "已完成" && len(old.Stability) == 0 {
			continue
		}
		if old.Status != "已完成" || len(old.Stability) > 0 {
			old.ID = fmt.Sprintf("round-%d-item-%d", i.CurrentRound, len(items)+1)
			old.Status = "待执行"
			old.CompletedAt = nil
			old.EffectReadingIDs = nil
			old.Evidence = ""
			old.Note = ""
			old.PausedAt = nil
			old.PauseReason = ""
			old.ExpectedResumeAt = nil
			items = append(items, old)
		}
	}
	if len(items) == 0 {
		items = []MitigationItem{{ID: itemID, Description: strings.TrimSpace(reason), Status: "待执行"}}
	}
	newPlan := MitigationPlan{ID: fmt.Sprintf("%s-plan-%d", i.ID, i.CurrentRound), IncidentID: i.ID, Summary: "根据复核退回原因整改", Owner: i.Assignee, DueAt: i.DueAt, SubmittedAt: &now, Round: i.CurrentRound, Items: items}
	i.Plan = &newPlan
	i.Rounds = append(i.Rounds, TreatmentRound{Number: i.CurrentRound, Plan: clonePlan(newPlan), StartedAt: now, ReturnedReason: strings.TrimSpace(reason)})
	i.appendEvent("退回处置", reviewer, req, map[string]interface{}{"from_round": previousRound, "to_round": i.CurrentRound, "reason": strings.TrimSpace(reason), "rectification_item_id": itemID, "source_round": previousRound, "return_category": returnCategory(reason)})
	return nil
}

// Verify 保留旧的领域签名，工作流会使用带服务端对比结果的新入口。
func (i *PreservationIncident) Verify(expected int, reviewer, decision, reason, req string) error {
	return i.VerifyWithComparisons(expected, reviewer, decision, reason, req, i.Comparisons)
}

func (i *PreservationIncident) appendEvent(eventType, actor, req string, payload map[string]interface{}) {
	seq := len(i.Timeline) + 1
	beforeStatus, afterStatus := eventStates(eventType, i.Status)
	beforeRevision, afterRevision := i.Revision-1, i.Revision
	if eventType == "登记与研判" {
		beforeRevision = 0
	}
	objectID := eventObjectID(eventType, payload)
	i.Timeline = append(i.Timeline, IncidentEvent{ID: fmt.Sprintf("%s-%d", i.ID, seq), IncidentID: i.ID, Sequence: seq, EventType: eventType, Actor: actor, OccurredAt: time.Now().UTC(), Payload: payload, RequestID: req, Round: i.CurrentRound, StatusBefore: beforeStatus, StatusAfter: afterStatus, RevisionBefore: beforeRevision, RevisionAfter: afterRevision, ObjectID: objectID})
}

func (i *PreservationIncident) SetRegistrationActor(actor, req string) {
	if len(i.Timeline) > 0 {
		i.Timeline[0].Actor, i.Timeline[0].RequestID = actor, req
	}
}

func (i *PreservationIncident) AppendManualReviewEvent(actor, req string, missing []string) {
	i.appendEvent("待人工复核", actor, req, map[string]interface{}{"missing": append([]string(nil), missing...)})
}

func (i *PreservationIncident) ConfirmManualReview(expected int, approve bool, actor, req string, now time.Time) error {
	if i.Revision != expected {
		return ErrConflict
	}
	if !i.PendingManualReview {
		return &ValidationError{Field: "manual_review", Message: "当前事件无需人工复核"}
	}
	if !approve {
		i.appendEvent("可信度驳回", actor, req, map[string]interface{}{"missing": i.ManualReviewMissing})
		return &ValidationError{Field: "manual_review", Message: "低可信读数未获确认，禁止分派"}
	}
	i.PendingManualReview = false
	i.ManualReviewMissing = nil
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("可信度确认", actor, req, map[string]interface{}{"risk": i.RiskLevel})
	return nil
}

func (i *PreservationIncident) syncCurrentRound() {
	if i.Plan != nil && len(i.Rounds) > 0 {
		i.Rounds[len(i.Rounds)-1].Plan = clonePlan(*i.Plan)
	}
}

func ptrTime(v time.Time) *time.Time { return &v }

func clonePlan(v MitigationPlan) MitigationPlan {
	v.Items = append([]MitigationItem(nil), v.Items...)
	for n := range v.Items {
		v.Items[n].EffectReadingIDs = append([]string(nil), v.Items[n].EffectReadingIDs...)
		v.Items[n].PrerequisiteIDs = append([]string(nil), v.Items[n].PrerequisiteIDs...)
		v.Items[n].BlockedBy = append([]string(nil), v.Items[n].BlockedBy...)
		v.Items[n].CoveredMetrics = append([]string(nil), v.Items[n].CoveredMetrics...)
		v.Items[n].ProcessRecords = append([]ProcessRecord(nil), v.Items[n].ProcessRecords...)
		for recordIndex := range v.Items[n].ProcessRecords {
			if v.Items[n].ProcessRecords[recordIndex].Reading != nil {
				reading := *v.Items[n].ProcessRecords[recordIndex].Reading
				v.Items[n].ProcessRecords[recordIndex].Reading = &reading
			}
		}
		v.Items[n].ProcessTrends = append([]ProcessTrend(nil), v.Items[n].ProcessTrends...)
		for trendIndex := range v.Items[n].ProcessTrends {
			v.Items[n].ProcessTrends[trendIndex].Points = append([]TrendPoint(nil), v.Items[n].ProcessTrends[trendIndex].Points...)
		}
		v.Items[n].Stability = cloneStability(v.Items[n].Stability)
	}
	v.Workload.Conflicts = append([]WorkloadEvent(nil), v.Workload.Conflicts...)
	return v
}

func cloneStability(values []StabilitySummary) []StabilitySummary {
	result := append([]StabilitySummary(nil), values...)
	for n := range result {
		result[n].ParticipatingReadings = append([]string(nil), result[n].ParticipatingReadings...)
		result[n].Trend = append([]TrendPoint(nil), result[n].Trend...)
	}
	return result
}

func cloneVerification(v *Verification) *Verification {
	if v == nil {
		return nil
	}
	cp := *v
	cp.ComparedReadingIDs = append([]string(nil), v.ComparedReadingIDs...)
	cp.Comparisons = append([]ReadingComparison(nil), v.Comparisons...)
	cp.ConfirmedReadingIDs = append([]string(nil), v.ConfirmedReadingIDs...)
	cp.ResponsibilityCheck.Recorders = append([]string(nil), v.ResponsibilityCheck.Recorders...)
	cp.MetricDecisions = append([]MetricVerification(nil), v.MetricDecisions...)
	for n := range cp.MetricDecisions {
		cp.MetricDecisions[n].ConfirmedReadingIDs = append([]string(nil), cp.MetricDecisions[n].ConfirmedReadingIDs...)
	}
	return &cp
}

func uniqueMetrics(rs []EnvironmentalReading) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		if !seen[r.Metric] {
			seen[r.Metric] = true
			out = append(out, r.Metric)
		}
	}
	sort.Strings(out)
	return out
}

func missingComparisonMetrics(cs []ReadingComparison) []string {
	var out []string
	for _, c := range cs {
		if c.EffectReadingID == "" || c.EffectValue == nil {
			out = append(out, c.Metric)
		}
	}
	return out
}

func comparisonIDs(cs []ReadingComparison) []string {
	seen := map[string]bool{}
	var ids []string
	for _, c := range cs {
		for _, id := range []string{c.BaselineReadingID, c.AbnormalReadingID, c.EffectReadingID} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}
