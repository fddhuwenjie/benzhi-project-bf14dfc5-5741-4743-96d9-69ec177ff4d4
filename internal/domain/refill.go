package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type AffectedCollectionItem struct {
	CollectionID      string `json:"collection_id"`
	Material          string `json:"material"`
	Quantity          int    `json:"quantity"`
	Sensitivity       string `json:"sensitivity"`
	ImpactNote        string `json:"impact_note"`
	CollectionNumber  string `json:"collection_number,omitempty"`
	MaterialCategory  string `json:"material_category,omitempty"`
	Count             int    `json:"count,omitempty"`
	SensitivityLevel  string `json:"sensitivity_level,omitempty"`
	ImpactDescription string `json:"impact_description,omitempty"`
}

type SupplementalObservation struct {
	Sequence   int                    `json:"sequence"`
	Note       string                 `json:"note"`
	Readings   []EnvironmentalReading `json:"readings"`
	ObservedAt time.Time              `json:"observed_at"`
	Actor      string                 `json:"actor"`
	RequestID  string                 `json:"request_id"`
}

type AssignmentCandidate struct {
	ID              string           `json:"id"`
	Assignee        string           `json:"assignee"`
	DueAt           time.Time        `json:"due_at"`
	Summary         string           `json:"summary"`
	Items           []MitigationItem `json:"items"`
	SelectionReason string           `json:"selection_reason"`
}

type AssignmentCandidateResult struct {
	ID               string   `json:"id"`
	Valid            bool     `json:"valid"`
	MissingMetrics   []string `json:"missing_metrics,omitempty"`
	DependencyIssues []string `json:"dependency_issues,omitempty"`
	WorkloadIssues   []string `json:"workload_issues,omitempty"`
	DeadlineIssues   []string `json:"deadline_issues,omitempty"`
}

type AssignmentPreview struct {
	IncidentID string                      `json:"incident_id"`
	Revision   int                         `json:"revision"`
	Checksum   string                      `json:"checksum"`
	Candidates []AssignmentCandidate       `json:"candidates"`
	Results    []AssignmentCandidateResult `json:"results"`
	CreatedAt  time.Time                   `json:"created_at"`
}

type AssignmentCandidateSummary struct {
	ID              string    `json:"id"`
	Assignee        string    `json:"assignee"`
	DueAt           time.Time `json:"due_at"`
	Summary         string    `json:"summary"`
	SelectionReason string    `json:"selection_reason"`
	Selected        bool      `json:"selected"`
}

type BatchRequestRecord struct {
	RequestID   string                  `json:"request_id"`
	Digest      string                  `json:"digest"`
	BatchID     string                  `json:"batch_id"`
	IncidentIDs []string                `json:"incident_ids"`
	Revisions   map[string]int          `json:"revisions"`
	Results     []*PreservationIncident `json:"results,omitempty"`
}

type BatchIncidentResult struct {
	IncidentID string `json:"incident_id"`
	Valid      bool   `json:"valid"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Status     Status `json:"status,omitempty"`
	Revision   int    `json:"revision,omitempty"`
}

type BatchConflictError struct {
	Results []BatchIncidentResult `json:"results"`
}

func (e *BatchConflictError) Error() string { return "批量分派预检未通过" }
func (e *BatchConflictError) Unwrap() error { return ErrConflict }

type ProcessRecord struct {
	Sequence    int                   `json:"sequence"`
	Type        string                `json:"type"`
	OccurredAt  time.Time             `json:"occurred_at"`
	Note        string                `json:"note"`
	Reading     *EnvironmentalReading `json:"reading,omitempty"`
	EvidenceRef string                `json:"evidence_ref,omitempty"`
	Actor       string                `json:"actor"`
	RequestID   string                `json:"request_id"`
}

type ProcessTrend struct {
	Metric    string       `json:"metric"`
	Points    []TrendPoint `json:"points"`
	Rebounded bool         `json:"rebounded"`
}

type PlanItemUpdate struct {
	ItemID          string   `json:"item_id"`
	Description     string   `json:"description,omitempty"`
	PrerequisiteIDs []string `json:"prerequisite_ids,omitempty"`
	CoveredMetrics  []string `json:"covered_metrics,omitempty"`
}

type PlanItemCancellation struct {
	ItemID string `json:"item_id"`
	Reason string `json:"reason"`
}

type PlanChange struct {
	Add    []MitigationItem       `json:"add,omitempty"`
	Update []PlanItemUpdate       `json:"update,omitempty"`
	Cancel []PlanItemCancellation `json:"cancel,omitempty"`
}

type PlanChangeAudit struct {
	ChangedAt time.Time        `json:"changed_at"`
	Reason    string           `json:"reason"`
	Approver  string           `json:"approver"`
	Before    []MitigationItem `json:"before"`
	After     []MitigationItem `json:"after"`
}

type DeadlineChangeRequest struct {
	ID                   string     `json:"id"`
	OriginalDueAt        time.Time  `json:"original_due_at"`
	RequestedDueAt       time.Time  `json:"requested_due_at"`
	Reason               string     `json:"reason"`
	AffectedItemIDs      []string   `json:"affected_item_ids"`
	Applicant            string     `json:"applicant"`
	RequestedAt          time.Time  `json:"requested_at"`
	OverdueWhenRequested bool       `json:"overdue_when_requested"`
	Status               string     `json:"status"`
	Decider              string     `json:"decider,omitempty"`
	DecisionNote         string     `json:"decision_note,omitempty"`
	DecidedAt            *time.Time `json:"decided_at,omitempty"`
}

type MetricVerification struct {
	Metric              string   `json:"metric"`
	Decision            string   `json:"decision"`
	ConfirmedReadingIDs []string `json:"confirmed_reading_ids"`
	Note                string   `json:"note"`
	EvidenceRef         string   `json:"evidence_ref"`
}

var supportedMaterials = map[string]bool{
	"纸质": true, "陶器": true, "陶瓷": true, "纺织品": true, "金属": true,
	"木质": true, "石质": true, "玻璃": true, "复合材质": true, "其他": true,
}

func IsSupportedMaterial(material string) bool {
	return supportedMaterials[strings.TrimSpace(material)]
}

func IsAffectedItemSensitivity(value string) bool {
	return sensitivityRank(value) > 0
}

func (f IncidentFilter) HasAffectedItemFilter() bool {
	return f.CollectionID != "" || f.Material != "" || f.ItemSensitivity != ""
}

func (f IncidentFilter) MatchesAffectedItem(item AffectedCollectionItem) bool {
	return (f.CollectionID == "" || item.CollectionID == f.CollectionID) &&
		(f.Material == "" || item.Material == f.Material) &&
		(f.ItemSensitivity == "" || item.Sensitivity == f.ItemSensitivity)
}

func (f IncidentFilter) MatchesAffectedItems(items []AffectedCollectionItem) bool {
	if !f.HasAffectedItemFilter() {
		return true
	}
	for _, item := range items {
		if f.MatchesAffectedItem(item) {
			return true
		}
	}
	return false
}

func ValidateAffectedItems(items []AffectedCollectionItem, declaredSensitivity string) ([]AffectedCollectionItem, string, []string, error) {
	if len(items) == 0 {
		return nil, "", nil, nil
	}
	seen := map[string]bool{}
	highest := "低"
	for n := range items {
		item := &items[n]
		if item.CollectionID == "" {
			item.CollectionID = item.CollectionNumber
		}
		if item.Material == "" {
			item.Material = item.MaterialCategory
		}
		if item.Quantity == 0 {
			item.Quantity = item.Count
		}
		if item.Sensitivity == "" {
			item.Sensitivity = item.SensitivityLevel
		}
		if item.ImpactNote == "" {
			item.ImpactNote = item.ImpactDescription
		}
		item.CollectionNumber, item.MaterialCategory, item.Count, item.SensitivityLevel, item.ImpactDescription = "", "", 0, "", ""
		item.CollectionID = strings.TrimSpace(item.CollectionID)
		item.Material = strings.TrimSpace(item.Material)
		item.Sensitivity = strings.TrimSpace(item.Sensitivity)
		item.ImpactNote = strings.TrimSpace(item.ImpactNote)
		prefix := fmt.Sprintf("affected_items[%d]", n)
		if item.CollectionID == "" {
			return nil, "", nil, &ValidationError{Field: prefix + ".collection_id", Message: "藏品编号不能为空"}
		}
		if seen[item.CollectionID] {
			return nil, "", nil, &ValidationError{Field: prefix + ".collection_id", Message: "藏品编号在本次登记中重复"}
		}
		seen[item.CollectionID] = true
		if item.Quantity <= 0 {
			return nil, "", nil, &ValidationError{Field: prefix + ".quantity", Message: "藏品数量必须为正数"}
		}
		if !supportedMaterials[item.Material] {
			return nil, "", nil, &ValidationError{Field: prefix + ".material", Message: "材质类别不受支持"}
		}
		if sensitivityRank(item.Sensitivity) == 0 {
			return nil, "", nil, &ValidationError{Field: prefix + ".sensitivity", Message: "敏感级别只能为高、中或低"}
		}
		if sensitivityRank(item.Sensitivity) > sensitivityRank(highest) {
			highest = item.Sensitivity
		}
		if utf8.RuneCountInString(item.ImpactNote) > 500 {
			return nil, "", nil, &ValidationError{Field: prefix + ".impact_note", Message: "影响说明不得超过 500 个字符"}
		}
	}
	if strings.TrimSpace(declaredSensitivity) != highest {
		return nil, "", nil, &ValidationError{Field: "sensitivity", Message: "总体敏感级别必须与藏品清单最高敏感级别一致"}
	}
	triggers := make([]string, 0)
	total := 0
	for _, item := range items {
		total += item.Quantity
		if item.Sensitivity == highest {
			triggers = append(triggers, item.CollectionID)
		}
	}
	sort.Strings(triggers)
	summary := fmt.Sprintf("%d 项藏品，共 %d 件（最高敏感级别%s：%s）", len(items), total, highest, strings.Join(triggers, "、"))
	return append([]AffectedCollectionItem(nil), items...), summary, triggers, nil
}

func sensitivityRank(value string) int {
	switch strings.TrimSpace(value) {
	case "低":
		return 1
	case "中":
		return 2
	case "高":
		return 3
	default:
		return 0
	}
}

func riskRank(value RiskLevel) int {
	switch value {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	default:
		return 0
	}
}

func (i *PreservationIncident) SetAffectedItems(items []AffectedCollectionItem, triggers []string) {
	i.AffectedItems = append([]AffectedCollectionItem(nil), items...)
	i.SensitivityTriggers = append([]string(nil), triggers...)
	if len(i.Timeline) > 0 && len(items) > 0 {
		i.Timeline[0].Payload["affected_items"] = i.AffectedItems
		i.Timeline[0].Payload["sensitivity_trigger_item_ids"] = i.SensitivityTriggers
	}
}

func (i *PreservationIncident) AddObservation(expected int, note string, readings []EnvironmentalReading, actor, requestID string, now time.Time, level RiskLevel, basis []string, response time.Duration, intervals []AbnormalInterval, pairings []BaselinePairing, missing []string, hits []RuleHit) error {
	if i.Status == StatusReview || i.Status == StatusClosed {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(strings.TrimSpace(note)) > 1000 {
		return &ValidationError{Field: "association_note", Message: "关联说明不能为空且不得超过 1000 个字符"}
	}
	if len(readings) == 0 {
		return &ValidationError{Field: "readings", Message: "至少需要一条新增观测读数"}
	}
	ids, refs := map[string]bool{}, map[string]bool{}
	for _, existing := range i.Readings {
		ids[existing.ID], refs[strings.TrimSpace(existing.EvidenceRef)] = true, true
	}
	observedAt := i.ObservedAt
	for n := range readings {
		reading := &readings[n]
		if reading.MeasuredAt.Before(i.ObservedAt) {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].measured_at", n), Message: "补充观测时间不得早于原事件观测时间"}
		}
		if ids[reading.ID] {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].id", n), Message: "读数标识已存在"}
		}
		if refs[strings.TrimSpace(reading.EvidenceRef)] {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].evidence_ref", n), Message: "证据引用已存在"}
		}
		ids[reading.ID], refs[strings.TrimSpace(reading.EvidenceRef)] = true, true
		reading.IncidentID, reading.Phase = i.ID, PhaseAbnormal
		if reading.MeasuredAt.After(observedAt) {
			observedAt = reading.MeasuredAt
		}
	}
	previousLevel, previousDue := i.RiskLevel, i.ResponseDue
	if riskRank(level) < riskRank(previousLevel) {
		level, basis, response = previousLevel, append([]string(nil), i.RiskBasis...), previousDue
	}
	if response > previousDue {
		response = previousDue
	}
	i.Readings = append(i.Readings, readings...)
	i.AdditionalObservations = append(i.AdditionalObservations, SupplementalObservation{Sequence: len(i.AdditionalObservations) + 1, Note: strings.TrimSpace(note), Readings: append([]EnvironmentalReading(nil), readings...), ObservedAt: observedAt, Actor: actor, RequestID: requestID})
	for _, reading := range readings {
		i.Evidence = append(i.Evidence, EvidenceSummary{ReadingID: reading.ID, Metric: reading.Metric, Reference: reading.EvidenceRef, SourceNote: reading.SourceNote, RecordedAt: reading.EvidenceRecordedAt})
	}
	i.RiskLevel, i.RiskBasis, i.ResponseDue = level, append([]string(nil), basis...), response
	i.AssessmentIntervals, i.BaselinePairings = append([]AbnormalInterval(nil), intervals...), append([]BaselinePairing(nil), pairings...)
	i.MissingBaselines, i.RuleHits = append([]string(nil), missing...), append([]RuleHit(nil), hits...)
	i.Revision++
	i.UpdatedAt = now
	i.RefreshDeadline(now)
	i.appendEvent("补充观测", actor, requestID, map[string]interface{}{"association_note": strings.TrimSpace(note), "readings": readings})
	if riskRank(level) > riskRank(previousLevel) {
		i.appendEvent("风险变更", actor, requestID, map[string]interface{}{"previous_risk_level": previousLevel, "risk_level": level, "risk_basis": basis, "previous_response_due": previousDue.String(), "response_due": response.String()})
	}
	return nil
}

func IncidentMetrics(i *PreservationIncident) []string {
	seen := map[string]bool{}
	for _, reading := range i.Readings {
		if reading.Phase == PhaseAbnormal && reading.ReplacedByID == "" {
			seen[reading.Metric] = true
		}
	}
	metrics := make([]string, 0, len(seen))
	for metric := range seen {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	return metrics
}

func coveredMetrics(items []MitigationItem) map[string]bool {
	covered := map[string]bool{}
	for _, item := range items {
		if item.CancelledAt != nil || item.Status == "已取消" {
			continue
		}
		for _, metric := range item.CoveredMetrics {
			covered[strings.TrimSpace(metric)] = true
		}
		for _, metric := range []string{"温度", "湿度", "光照", "污染物"} {
			if strings.Contains(item.Description, metric) {
				covered[metric] = true
			}
		}
	}
	return covered
}

func ValidatePlanCoverage(metrics []string, items []MitigationItem) []string {
	covered := coveredMetrics(items)
	missing := make([]string, 0)
	for _, metric := range metrics {
		if !covered[metric] {
			missing = append(missing, metric)
		}
	}
	sort.Strings(missing)
	return missing
}

func ValidatePlanDependencies(items []MitigationItem) error { return validateDependencies(items) }

func ValidateAssignmentCandidates(i *PreservationIncident, candidates []AssignmentCandidate, now time.Time) ([]AssignmentCandidateResult, error) {
	if i.Status != StatusPending {
		return nil, ErrState
	}
	if len(candidates) < 2 || len(candidates) > 3 {
		return nil, &ValidationError{Field: "candidates", Message: "候选方案数量必须为二至三个"}
	}
	results := make([]AssignmentCandidateResult, len(candidates))
	seen := map[string]bool{}
	metrics := IncidentMetrics(i)
	for n, candidate := range candidates {
		result := AssignmentCandidateResult{ID: candidate.ID}
		if strings.TrimSpace(candidate.ID) == "" || seen[candidate.ID] {
			return nil, &ValidationError{Field: fmt.Sprintf("candidates[%d].id", n), Message: "候选方案编号不能为空且不得重复"}
		}
		seen[candidate.ID] = true
		if strings.TrimSpace(candidate.Assignee) == "" {
			result.WorkloadIssues = append(result.WorkloadIssues, "执行人不能为空")
		}
		if strings.TrimSpace(candidate.SelectionReason) == "" {
			result.WorkloadIssues = append(result.WorkloadIssues, "选择说明不能为空")
		}
		result.MissingMetrics = ValidatePlanCoverage(metrics, candidate.Items)
		if err := validateDependencies(candidate.Items); err != nil {
			result.DependencyIssues = append(result.DependencyIssues, err.Error())
		}
		latest := i.ObservedAt.Add(i.ResponseDue)
		if candidate.DueAt.IsZero() || candidate.DueAt.Before(now) {
			result.DeadlineIssues = append(result.DeadlineIssues, "期限不得早于当前时间")
		}
		if now.Before(latest) && candidate.DueAt.After(latest) {
			result.DeadlineIssues = append(result.DeadlineIssues, "期限超过建议响应时限")
		}
		if !now.Before(latest) && candidate.DueAt.After(now.Add(i.ResponseDue)) {
			result.DeadlineIssues = append(result.DeadlineIssues, "逾期事件期限超过重新计算的响应时限")
		}
		result.Valid = len(result.MissingMetrics)+len(result.DependencyIssues)+len(result.WorkloadIssues)+len(result.DeadlineIssues) == 0
		results[n] = result
	}
	sort.SliceStable(results, func(a, b int) bool { return results[a].ID < results[b].ID })
	return results, nil
}

func AssignmentCandidatesChecksum(incidentID string, revision int, candidates []AssignmentCandidate) string {
	cp := append([]AssignmentCandidate(nil), candidates...)
	sort.SliceStable(cp, func(a, b int) bool { return cp[a].ID < cp[b].ID })
	b, _ := json.Marshal(struct {
		IncidentID string
		Revision   int
		Candidates []AssignmentCandidate
	}{incidentID, revision, cp})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (i *PreservationIncident) SetAssignmentCandidates(candidates []AssignmentCandidate, selectedID string) {
	i.AssignmentCandidates = nil
	for _, candidate := range candidates {
		i.AssignmentCandidates = append(i.AssignmentCandidates, AssignmentCandidateSummary{ID: candidate.ID, Assignee: candidate.Assignee, DueAt: candidate.DueAt, Summary: candidate.Summary, SelectionReason: candidate.SelectionReason, Selected: candidate.ID == selectedID})
	}
	if len(i.Timeline) > 0 && i.Timeline[len(i.Timeline)-1].EventType == "分派" {
		i.Timeline[len(i.Timeline)-1].Payload["candidate_summaries"] = i.AssignmentCandidates
	}
}

func (i *PreservationIncident) ChangePlan(expected int, change PlanChange, reason, approver, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil || i.Plan.SubmittedAt == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(approver) == "" {
		return &ValidationError{Field: "reason", Message: "变更原因和负责人不能为空"}
	}
	before := clonePlan(*i.Plan).Items
	items := clonePlan(*i.Plan).Items
	index := map[string]int{}
	for n, item := range items {
		index[item.ID] = n
	}
	mutable := func(item MitigationItem) bool {
		return item.Status == "待执行" && item.PausedAt == nil && len(item.ProcessRecords) == 0 && item.CompletedAt == nil
	}
	for n, update := range change.Update {
		idx, ok := index[update.ItemID]
		if !ok {
			return &ValidationError{Field: fmt.Sprintf("update[%d].item_id", n), Message: "措施项不存在"}
		}
		if !mutable(items[idx]) {
			return &ValidationError{Field: fmt.Sprintf("update[%d].item_id", n), Message: "已完成、已暂停或已有过程记录的措施不可改写"}
		}
		if strings.TrimSpace(update.Description) != "" {
			items[idx].Description = strings.TrimSpace(update.Description)
		}
		if update.PrerequisiteIDs != nil {
			items[idx].PrerequisiteIDs = append([]string(nil), update.PrerequisiteIDs...)
		}
		if update.CoveredMetrics != nil {
			items[idx].CoveredMetrics = append([]string(nil), update.CoveredMetrics...)
		}
	}
	for n, cancellation := range change.Cancel {
		idx, ok := index[cancellation.ItemID]
		if !ok {
			return &ValidationError{Field: fmt.Sprintf("cancel[%d].item_id", n), Message: "措施项不存在"}
		}
		if !mutable(items[idx]) {
			return &ValidationError{Field: fmt.Sprintf("cancel[%d].item_id", n), Message: "已有过程记录、已完成或已暂停的措施不可取消"}
		}
		if strings.TrimSpace(cancellation.Reason) == "" {
			return &ValidationError{Field: fmt.Sprintf("cancel[%d].reason", n), Message: "取消原因不能为空"}
		}
		items[idx].Status, items[idx].CancellationReason, items[idx].CancelledAt = "已取消", strings.TrimSpace(cancellation.Reason), ptrTime(now)
	}
	for n, item := range change.Add {
		if strings.TrimSpace(item.ID) == "" {
			item.ID = fmt.Sprintf("%s-r%d-change-%d", i.ID, i.CurrentRound, n+1)
		}
		if _, ok := index[item.ID]; ok {
			return &ValidationError{Field: fmt.Sprintf("add[%d].id", n), Message: "措施项编号已存在"}
		}
		if strings.TrimSpace(item.Description) == "" {
			return &ValidationError{Field: fmt.Sprintf("add[%d].description", n), Message: "措施说明不能为空"}
		}
		item.Status = "待执行"
		index[item.ID] = len(items)
		items = append(items, item)
	}
	active := make([]MitigationItem, 0, len(items))
	for _, item := range items {
		if item.Status != "已取消" {
			active = append(active, item)
		}
	}
	if err := validateDependencies(active); err != nil {
		return err
	}
	if missing := ValidatePlanCoverage(IncidentMetrics(i), active); len(missing) > 0 {
		return &ValidationError{Field: "items.covered_metrics", Message: "变更后存在未覆盖的异常指标", MissingMetrics: missing}
	}
	for n, item := range active {
		if item.Status == "已完成" && len(incompletePrerequisites(active, item)) > 0 {
			return &ValidationError{Field: fmt.Sprintf("items[%d].prerequisite_ids", n), Message: "新依赖使已完成顺序失效"}
		}
	}
	if i.DueAt.Before(now) {
		return &ValidationError{Field: "due_at", Message: "当前期限已不可达，请先完成期限变更审批"}
	}
	i.Plan.Items = items
	refreshPlanProgress(i.Plan)
	i.Revision++
	i.UpdatedAt = now
	i.syncCurrentRound()
	audit := PlanChangeAudit{ChangedAt: now, Reason: strings.TrimSpace(reason), Approver: strings.TrimSpace(approver), Before: before, After: clonePlan(*i.Plan).Items}
	i.PlanChanges = append(i.PlanChanges, audit)
	i.appendEvent("方案变更", approver, requestID, map[string]interface{}{"reason": audit.Reason, "approver": audit.Approver, "before": before, "after": audit.After})
	return nil
}

func normalizeRecordType(value string) string {
	switch strings.TrimSpace(value) {
	case "start", "开始":
		return "开始"
	case "checkpoint", "检查点":
		return "检查点"
	case "issue", "问题":
		return "问题"
	case "resolved", "问题解决":
		return "问题解决"
	default:
		return ""
	}
}

func (i *PreservationIncident) AddProcessRecord(expected int, itemID string, record ProcessRecord, actor, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	idx := -1
	for n := range i.Plan.Items {
		if i.Plan.Items[n].ID == itemID {
			idx = n
			break
		}
	}
	if idx < 0 {
		return &ValidationError{Field: "item_id", Message: "措施项不存在"}
	}
	item := &i.Plan.Items[idx]
	if item.Status == "已完成" || item.Status == "已取消" {
		return ErrState
	}
	if item.PausedAt != nil {
		return ErrState
	}
	record.Type = normalizeRecordType(record.Type)
	if record.Type == "" {
		return &ValidationError{Field: "type", Message: "过程记录类型只能为开始、检查点、问题或问题解决"}
	}
	if strings.TrimSpace(record.Note) == "" {
		return &ValidationError{Field: "note", Message: "过程说明不能为空"}
	}
	assignmentAt := i.Rounds[len(i.Rounds)-1].StartedAt
	if record.OccurredAt.Before(assignmentAt) || record.OccurredAt.After(now) {
		return &ValidationError{Field: "occurred_at", Message: "发生时间必须介于本轮分派与提交之间"}
	}
	if len(item.ProcessRecords) > 0 && record.OccurredAt.Before(item.ProcessRecords[len(item.ProcessRecords)-1].OccurredAt) {
		return &ValidationError{Field: "occurred_at", Message: "过程记录时间不得倒序"}
	}
	started, openIssue := false, false
	for _, existing := range item.ProcessRecords {
		if existing.Type == "开始" {
			started = true
		}
		if existing.Type == "问题" {
			openIssue = true
		}
		if existing.Type == "问题解决" {
			openIssue = false
		}
	}
	if record.Type == "开始" && started {
		return &ValidationError{Field: "type", Message: "开始记录只能追加一次"}
	}
	if record.Type != "开始" && !started {
		return &ValidationError{Field: "type", Message: "必须先追加开始记录"}
	}
	if record.Type == "问题解决" && !openIssue {
		return &ValidationError{Field: "type", Message: "没有待解决问题"}
	}
	if record.Type == "问题" && openIssue {
		return &ValidationError{Field: "type", Message: "上一问题尚未解决"}
	}
	ref := strings.TrimSpace(record.EvidenceRef)
	if record.Reading != nil && ref == "" {
		ref = strings.TrimSpace(record.Reading.EvidenceRef)
	}
	if ref != "" {
		for _, reading := range i.Readings {
			if strings.TrimSpace(reading.EvidenceRef) == ref {
				return &ValidationError{Field: "evidence_ref", Message: "证据引用已存在"}
			}
		}
		for _, candidate := range i.Plan.Items {
			for _, existing := range candidate.ProcessRecords {
				if existing.EvidenceRef == ref {
					return &ValidationError{Field: "evidence_ref", Message: "证据引用已存在"}
				}
			}
		}
	}
	if record.Reading != nil {
		if record.Reading.MeasuredAt.IsZero() {
			record.Reading.MeasuredAt = record.OccurredAt
		}
		if record.Reading.MeasuredAt.Before(assignmentAt) || record.Reading.MeasuredAt.After(now) {
			return &ValidationError{Field: "reading.measured_at", Message: "中间读数时间必须介于本轮分派与提交之间"}
		}
		record.Reading.Phase, record.Reading.IncidentID = PhaseEffect, i.ID
		record.Reading.EvidenceRef = ref
	}
	record.Sequence, record.Actor, record.RequestID, record.EvidenceRef = len(item.ProcessRecords)+1, actor, requestID, ref
	item.ProcessRecords = append(item.ProcessRecords, record)
	refreshProcessTrends(item, i.RuleSnapshot)
	i.Revision++
	i.UpdatedAt = now
	i.syncCurrentRound()
	i.appendEvent("措施过程记录", actor, requestID, map[string]interface{}{"item_id": itemID, "record": record})
	return nil
}

func refreshProcessTrends(item *MitigationItem, rules RuleSnapshot) {
	groups := map[string][]EnvironmentalReading{}
	for _, record := range item.ProcessRecords {
		if record.Reading != nil {
			groups[record.Reading.Metric] = append(groups[record.Reading.Metric], *record.Reading)
		}
	}
	metrics := make([]string, 0, len(groups))
	for metric := range groups {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	item.ProcessTrends = nil
	for _, metric := range metrics {
		readings := groups[metric]
		sort.SliceStable(readings, func(a, b int) bool { return readings[a].MeasuredAt.Before(readings[b].MeasuredAt) })
		trend := ProcessTrend{Metric: metric}
		previousWithin := false
		for n, reading := range readings {
			within := withinSnapshot(reading, rules)
			point := TrendPoint{ReadingID: reading.ID, MeasuredAt: reading.MeasuredAt, Value: reading.Value, Unit: reading.Unit, WithinThreshold: within}
			if n > 0 {
				change := reading.Value - readings[n-1].Value
				point.ChangeFromPrev = &change
				if previousWithin && !within {
					trend.Rebounded = true
				}
			}
			trend.Points = append(trend.Points, point)
			previousWithin = within
		}
		item.ProcessTrends = append(item.ProcessTrends, trend)
	}
}

func withinSnapshot(reading EnvironmentalReading, rules RuleSnapshot) bool {
	switch reading.Metric {
	case "温度":
		return reading.Value >= rules.TemperatureMin && reading.Value <= rules.TemperatureMax
	case "湿度":
		return reading.Value >= rules.HumidityMin && reading.Value <= rules.HumidityMax
	case "光照":
		return reading.Value <= rules.LightMax
	case "污染物":
		return reading.Value <= rules.PollutantMax
	}
	return false
}

func (i *PreservationIncident) ValidateCompletionRecords(itemID string, sequences []int) error {
	for _, item := range i.Plan.Items {
		if item.ID != itemID {
			continue
		}
		if len(sequences) == 0 {
			return &ValidationError{Field: "process_record_sequences", Message: "最终完成必须引用至少一条过程记录"}
		}
		valid := map[int]bool{}
		for _, record := range item.ProcessRecords {
			valid[record.Sequence] = true
		}
		for n, sequence := range sequences {
			if !valid[sequence] {
				return &ValidationError{Field: fmt.Sprintf("process_record_sequences[%d]", n), Message: "引用的过程记录不属于当前措施"}
			}
		}
		return nil
	}
	return &ValidationError{Field: "item_id", Message: "措施项不存在"}
}

func (i *PreservationIncident) RequestDeadlineChange(expected int, requested time.Time, reason string, affected []string, applicant, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(applicant) == "" || strings.TrimSpace(applicant) != i.Assignee {
		return &ValidationError{Field: "applicant", Message: "只有当前执行人可以申请期限变更"}
	}
	if i.PendingDeadlineChange != nil {
		return &ValidationError{Field: "deadline_change", Message: "当前已有待决定的期限变更申请"}
	}
	if !requested.After(i.DueAt) {
		return &ValidationError{Field: "requested_due_at", Message: "新期限必须晚于当前期限"}
	}
	if strings.TrimSpace(reason) == "" {
		return &ValidationError{Field: "reason", Message: "延期原因不能为空"}
	}
	if len(affected) == 0 {
		return &ValidationError{Field: "affected_item_ids", Message: "至少需要选择一项受延期影响的措施"}
	}
	valid := map[string]bool{}
	for _, item := range i.Plan.Items {
		valid[item.ID] = item.Status != "已取消"
	}
	for n, id := range affected {
		if !valid[id] {
			return &ValidationError{Field: fmt.Sprintf("affected_item_ids[%d]", n), Message: "受影响措施不存在或已取消"}
		}
	}
	req := DeadlineChangeRequest{ID: fmt.Sprintf("%s-deadline-%d", i.ID, len(i.DeadlineChangeHistory)+1), OriginalDueAt: i.DueAt, RequestedDueAt: requested, Reason: strings.TrimSpace(reason), AffectedItemIDs: append([]string(nil), affected...), Applicant: applicant, RequestedAt: now, OverdueWhenRequested: now.After(i.DueAt), Status: "待审批"}
	i.PendingDeadlineChange = &req
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("期限变更申请", applicant, requestID, map[string]interface{}{"request": req})
	return nil
}

func (i *PreservationIncident) DecideDeadlineChange(expected int, approve bool, decider, note, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.PendingDeadlineChange == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(decider) == "" || strings.TrimSpace(decider) == i.PendingDeadlineChange.Applicant {
		return &ValidationError{Field: "decider", Message: "申请人不得代替负责人作出决定"}
	}
	if strings.TrimSpace(note) == "" {
		return &ValidationError{Field: "decision_note", Message: "决定说明不能为空"}
	}
	req := *i.PendingDeadlineChange
	req.Decider, req.DecisionNote, req.DecidedAt = strings.TrimSpace(decider), strings.TrimSpace(note), ptrTime(now)
	if approve {
		req.Status = "已批准"
		i.DueAt = req.RequestedDueAt
		i.Plan.DueAt = req.RequestedDueAt
		i.DeadlineChangeCount++
	} else {
		req.Status = "已驳回"
	}
	i.DeadlineChangeHistory = append(i.DeadlineChangeHistory, req)
	i.PendingDeadlineChange = nil
	i.Revision++
	i.UpdatedAt = now
	i.syncCurrentRound()
	i.appendEvent("期限变更决定", decider, requestID, map[string]interface{}{"request": req, "current_due_at": i.DueAt})
	return nil
}

func (i *PreservationIncident) ValidateMetricVerification(decisions []MetricVerification, overall string) ([]string, error) {
	metrics := IncidentMetrics(i)
	validReadings := map[string]EnvironmentalReading{}
	for _, reading := range i.Readings {
		if reading.ReplacedByID == "" {
			validReadings[reading.ID] = reading
		}
	}
	byMetric := map[string]MetricVerification{}
	failures := []string{}
	for n := range decisions {
		decision := &decisions[n]
		decision.Metric, decision.Note, decision.EvidenceRef = strings.TrimSpace(decision.Metric), strings.TrimSpace(decision.Note), strings.TrimSpace(decision.EvidenceRef)
		if _, exists := byMetric[decision.Metric]; exists {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].metric", n), Message: "异常指标判定不得重复"}
		}
		if decision.Decision != "合格" && decision.Decision != "不合格" {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].decision", n), Message: "逐项判定只能为合格或不合格"}
		}
		if decision.Note == "" {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].note", n), Message: "现场核验说明不能为空"}
		}
		if decision.EvidenceRef == "" {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].evidence_ref", n), Message: "核验证据不能为空"}
		}
		if len(decision.ConfirmedReadingIDs) == 0 {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].confirmed_reading_ids", n), Message: "必须确认参与比较的读数"}
		}
		evidenceMatched := false
		for k, id := range decision.ConfirmedReadingIDs {
			reading, ok := validReadings[id]
			if !ok || reading.Metric != decision.Metric {
				return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].confirmed_reading_ids[%d]", n, k), Message: "确认读数不属于当前指标有效比较集"}
			}
			if reading.EvidenceRef == decision.EvidenceRef {
				evidenceMatched = true
			}
		}
		if !evidenceMatched {
			return nil, &ValidationError{Field: fmt.Sprintf("metric_decisions[%d].evidence_ref", n), Message: "核验证据必须来自当前有效比较读数"}
		}
		byMetric[decision.Metric] = *decision
		if decision.Decision == "不合格" {
			failures = append(failures, decision.Metric)
		}
	}
	for _, metric := range metrics {
		if _, ok := byMetric[metric]; !ok {
			return nil, &ValidationError{Field: "metric_decisions", Message: "所有异常指标均必须逐项判定", MissingMetrics: []string{metric}}
		}
	}
	sort.Strings(failures)
	if len(failures) == 0 && overall != "合格" {
		return nil, &ValidationError{Field: "decision", Message: "全部指标合格时总体结论只能为合格"}
	}
	if len(failures) > 0 && overall != "退回" {
		return nil, &ValidationError{Field: "decision", Message: "存在不合格指标时总体结论只能为退回", MissingMetrics: failures}
	}
	return failures, nil
}
