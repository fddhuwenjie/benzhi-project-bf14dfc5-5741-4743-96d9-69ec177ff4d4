package workflow

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"time"
)

func requireRequestID(requestID string) error {
	if strings.TrimSpace(requestID) == "" {
		return &domain.ValidationError{Field: "request_id", Message: "request_id 不能为空"}
	}
	return nil
}

func (s *Service) AddObservation(id string, revision int, readings []domain.EnvironmentalReading, note, actor, requestID string) (*domain.PreservationIncident, error) {
	return s.AddObservationInArea(id, revision, "", readings, note, actor, requestID)
}

func (s *Service) AddObservationInArea(id string, revision int, areaID string, readings []domain.EnvironmentalReading, note, actor, requestID string) (*domain.PreservationIncident, error) {
	for n, reading := range readings {
		if reading.Phase == domain.PhaseBaseline {
			return s.BackfillBaselines(id, revision, areaID, readings, actor, requestID)
		}
		if reading.Phase != "" && reading.Phase != domain.PhaseAbnormal {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("readings[%d].phase", n), Message: "补充观测读数的 phase 只能为 abnormal 或 baseline"}
		}
	}
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Readings    []domain.EnvironmentalReading
		Note, Actor string
	}{readings, note, actor})
	if in, handled, err := s.reuse(requestID, "observation", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Revision != revision {
		return nil, domain.ErrConflict
	}
	if strings.TrimSpace(areaID) != "" && strings.TrimSpace(areaID) != in.AreaID {
		return nil, &domain.ValidationError{Field: "area_id", Message: "补充观测保存区域必须与目标事件一致"}
	}
	if in.Status == domain.StatusReview || in.Status == domain.StatusClosed {
		return nil, domain.ErrState
	}
	now := s.now()
	normalized := make([]domain.EnvironmentalReading, len(readings))
	latest := in.ObservedAt
	for n, reading := range readings {
		reading.Phase = domain.PhaseAbnormal
		if reading.EvidenceRecordedAt.IsZero() {
			reading.EvidenceRecordedAt = now
		}
		if reading.MeasuredAt.Before(in.ObservedAt) {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("readings[%d].measured_at", n), Message: "补充观测时间不得早于原事件观测时间"}
		}
		if reading.MeasuredAt.After(latest) {
			latest = reading.MeasuredAt
		}
		normalized[n], err = assessment.Normalize(reading)
		if err != nil {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("readings[%d].unit", n), Message: err.Error()}
		}
	}
	active := make([]domain.EnvironmentalReading, 0, len(in.Readings)+len(normalized))
	for _, reading := range in.Readings {
		if reading.ReplacedByID == "" && reading.Phase != domain.PhaseEffect {
			active = append(active, reading)
		}
	}
	active = append(active, normalized...)
	result, err := assessment.EvaluateAt(active, in.Sensitivity, latest, now, lockedRules(in, s.Rules))
	if err != nil {
		return nil, err
	}
	if len(in.SensitivityTriggers) > 0 {
		result.Basis = append(result.Basis, "最高敏感级别藏品: "+strings.Join(in.SensitivityTriggers, "、"))
	}
	if err = in.AddObservation(revision, note, normalized, actor, requestID, now, result.Level, result.Basis, result.Response, result.Intervals, result.Pairings, result.MissingBaselines, result.RuleHits); err != nil {
		return nil, err
	}
	in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	return s.commit(in, revision, requestID, "observation", digest)
}

func (s *Service) PreviewAssignment(id string, revision int, candidates []domain.AssignmentCandidate) (domain.AssignmentPreview, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return domain.AssignmentPreview{}, err
	}
	if in.Revision != revision {
		return domain.AssignmentPreview{}, domain.ErrConflict
	}
	results, err := domain.ValidateAssignmentCandidates(in, candidates, s.now())
	if err != nil {
		return domain.AssignmentPreview{}, err
	}
	byID := map[string]int{}
	for n := range results {
		byID[results[n].ID] = n
	}
	for _, candidate := range candidates {
		snapshot := s.workloadSnapshot(in, candidate.Assignee, candidate.DueAt, "")
		if len(snapshot.Conflicts) > 0 {
			idx := byID[candidate.ID]
			results[idx].Valid = false
			results[idx].WorkloadIssues = append(results[idx].WorkloadIssues, "执行人的活动任务期限发生冲突")
		}
	}
	return domain.AssignmentPreview{IncidentID: id, Revision: revision, Checksum: domain.AssignmentCandidatesChecksum(id, revision, candidates), Candidates: candidates, Results: results, CreatedAt: s.now()}, nil
}

func (s *Service) ConfirmAssignmentCandidate(id string, revision int, candidates []domain.AssignmentCandidate, selectedID, checksum, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Candidates                  []domain.AssignmentCandidate
		SelectedID, Checksum, Actor string
	}{candidates, selectedID, checksum, actor})
	if in, handled, err := s.reuse(requestID, "assignment-candidate", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Revision != revision {
		return nil, domain.ErrConflict
	}
	if checksum == "" || checksum != domain.AssignmentCandidatesChecksum(id, revision, candidates) {
		return nil, &domain.ValidationError{Field: "preview_checksum", Message: "候选方案内容与预览不一致，预览已失效"}
	}
	preview, err := s.PreviewAssignment(id, revision, candidates)
	if err != nil {
		return nil, err
	}
	valid := map[string]bool{}
	for _, result := range preview.Results {
		valid[result.ID] = result.Valid
	}
	if !valid[selectedID] {
		return nil, &domain.ValidationError{Field: "selected_candidate_id", Message: "只能确认通过校验的候选方案"}
	}
	var selected *domain.AssignmentCandidate
	for n := range candidates {
		if candidates[n].ID == selectedID {
			selected = &candidates[n]
			break
		}
	}
	if selected == nil {
		return nil, &domain.ValidationError{Field: "selected_candidate_id", Message: "选中的候选方案不存在"}
	}
	snapshot := s.workloadSnapshot(in, selected.Assignee, selected.DueAt, "")
	plan := domain.MitigationPlan{Summary: selected.Summary, Owner: selected.Assignee, DueAt: selected.DueAt, Items: selected.Items, Workload: snapshot}
	overdueNote := ""
	if s.now().After(in.ObservedAt.Add(in.ResponseDue)) {
		overdueNote = selected.SelectionReason
	}
	if err = in.AssignWithDeadline(revision, selected.Assignee, selected.DueAt, plan, actor, requestID, overdueNote, s.now()); err != nil {
		return nil, err
	}
	in.SetAssignmentCandidates(candidates, selectedID)
	return s.commit(in, revision, requestID, "assignment-candidate", digest)
}

type BatchAssignmentEntry struct {
	IncidentID       string `json:"incident_id"`
	ExpectedRevision int    `json:"expected_revision"`
}
type BatchAssignmentCommand struct {
	Entries     []BatchAssignmentEntry  `json:"entries"`
	Assignee    string                  `json:"assignee"`
	DueAt       time.Time               `json:"due_at,omitempty"`
	DueAfter    time.Duration           `json:"due_after,omitempty"`
	Summary     string                  `json:"summary"`
	Items       []domain.MitigationItem `json:"items"`
	Actor       string                  `json:"actor"`
	RequestID   string                  `json:"request_id"`
	OverdueNote string                  `json:"overdue_note,omitempty"`
}
type BatchAssignmentResult struct {
	BatchID   string                         `json:"batch_id"`
	Results   []domain.BatchIncidentResult   `json:"results"`
	Incidents []*domain.PreservationIncident `json:"incidents,omitempty"`
}

func (s *Service) PreflightBatchAssignment(command BatchAssignmentCommand) BatchAssignmentResult {
	result := BatchAssignmentResult{BatchID: command.RequestID}
	if len(command.Entries) == 0 {
		result.Results = append(result.Results, domain.BatchIncidentResult{Code: "validation_error", Message: "至少选择一个待分派事件"})
		return result
	}
	entries := append([]BatchAssignmentEntry(nil), command.Entries...)
	sort.Slice(entries, func(a, b int) bool { return entries[a].IncidentID < entries[b].IncidentID })
	seen := map[string]bool{}
	for _, entry := range entries {
		item := domain.BatchIncidentResult{IncidentID: entry.IncidentID}
		in, err := s.Repo.Get(entry.IncidentID)
		if err != nil {
			item.Code, item.Message = "not_found", "事件不存在"
			result.Results = append(result.Results, item)
			continue
		}
		item.Status, item.Revision = in.Status, in.Revision
		if seen[entry.IncidentID] {
			item.Code, item.Message = "duplicate_incident", "事件编号重复"
		} else if in.Revision != entry.ExpectedRevision {
			item.Code, item.Message = "revision_conflict", "事件修订号已变化"
		} else if in.Status != domain.StatusPending {
			item.Code, item.Message = "invalid_state", "事件不再处于待分派"
		} else if strings.TrimSpace(command.Assignee) == "" {
			item.Code, item.Message = "assignee_required", "统一执行人不能为空"
		} else if len(command.Items) == 0 {
			item.Code, item.Message = "template_required", "措施模板不能为空"
		} else if dependencyErr := domain.ValidatePlanDependencies(command.Items); dependencyErr != nil {
			item.Code, item.Message = "template_dependency_invalid", dependencyErr.Error()
		} else if missing := domain.ValidatePlanCoverage(domain.IncidentMetrics(in), command.Items); len(missing) > 0 {
			item.Code, item.Message = "template_not_applicable", "措施模板未覆盖指标: "+strings.Join(missing, "、")
		} else if err = validateBatchDeadline(in, command, s.now()); err != nil {
			item.Code, item.Message = "deadline_invalid", err.Error()
		} else {
			due := batchDue(in, command, s.now())
			snapshot := s.workloadSnapshot(in, command.Assignee, due, "")
			if len(snapshot.Conflicts) > 0 {
				item.Code, item.Message = "workload_conflict", "执行人工作量冲突"
			} else {
				item.Valid = true
			}
		}
		seen[entry.IncidentID] = true
		result.Results = append(result.Results, item)
	}
	return result
}

func batchDue(in *domain.PreservationIncident, command BatchAssignmentCommand, now time.Time) time.Time {
	if !command.DueAt.IsZero() {
		return command.DueAt
	}
	if command.DueAfter > 0 {
		return now.Add(command.DueAfter)
	}
	latest := in.ObservedAt.Add(in.ResponseDue)
	if now.After(latest) {
		return now.Add(in.ResponseDue)
	}
	return latest
}
func validateBatchDeadline(in *domain.PreservationIncident, command BatchAssignmentCommand, now time.Time) error {
	due := batchDue(in, command, now)
	if due.Before(now) {
		return fmt.Errorf("期限不得早于当前时间")
	}
	if now.Before(in.ObservedAt.Add(in.ResponseDue)) && due.After(in.ObservedAt.Add(in.ResponseDue)) {
		return fmt.Errorf("期限超过建议响应时限")
	}
	if !now.Before(in.ObservedAt.Add(in.ResponseDue)) {
		if strings.TrimSpace(command.OverdueNote) == "" {
			return fmt.Errorf("逾期事件必须提供逾期说明")
		}
		if due.After(now.Add(in.ResponseDue)) {
			return fmt.Errorf("逾期事件期限超过重新计算的响应时限")
		}
	}
	return nil
}

func (s *Service) AssignBatch(command BatchAssignmentCommand) (BatchAssignmentResult, error) {
	if err := requireRequestID(command.RequestID); err != nil {
		return BatchAssignmentResult{}, err
	}
	digest := requestDigest(command)
	if old, ok := s.Repo.FindBatchRequest(command.RequestID); ok {
		if old.Digest != digest {
			return BatchAssignmentResult{}, &domain.IdempotencyConflictError{}
		}
		result := BatchAssignmentResult{BatchID: old.BatchID}
		incidents := old.Results
		if len(incidents) == 0 && len(old.IncidentIDs) > 0 {
			incidents = make([]*domain.PreservationIncident, 0, len(old.IncidentIDs))
			for _, id := range old.IncidentIDs {
				in, err := s.Repo.Get(id)
				if err != nil {
					return BatchAssignmentResult{}, err
				}
				incidents = append(incidents, in)
			}
		}
		for _, in := range incidents {
			result.Incidents = append(result.Incidents, in)
			result.Results = append(result.Results, domain.BatchIncidentResult{IncidentID: in.ID, Valid: true, Status: in.Status, Revision: in.Revision})
		}
		sort.Slice(result.Results, func(a, b int) bool { return result.Results[a].IncidentID < result.Results[b].IncidentID })
		return result, nil
	}
	preflight := s.PreflightBatchAssignment(command)
	for _, result := range preflight.Results {
		if !result.Valid {
			return preflight, &domain.BatchConflictError{Results: preflight.Results}
		}
	}
	now := s.now()
	expected := map[string]int{}
	revisions := map[string]int{}
	incidents := make([]*domain.PreservationIncident, 0, len(command.Entries))
	ids := make([]string, 0, len(command.Entries))
	for _, entry := range command.Entries {
		in, err := s.Repo.Get(entry.IncidentID)
		if err != nil {
			return preflight, err
		}
		due := batchDue(in, command, now)
		snapshot := s.workloadSnapshot(in, command.Assignee, due, "")
		plan := domain.MitigationPlan{Summary: command.Summary, Owner: command.Assignee, DueAt: due, Items: append([]domain.MitigationItem(nil), command.Items...), Workload: snapshot}
		if err = in.AssignWithDeadline(entry.ExpectedRevision, command.Assignee, due, plan, command.Actor, command.RequestID, command.OverdueNote, now); err != nil {
			return preflight, err
		}
		for n := len(in.Timeline) - 1; n >= 0; n-- {
			if in.Timeline[n].EventType == "分派" {
				in.Timeline[n].Payload["batch_id"] = command.RequestID
				break
			}
		}
		expected[in.ID], revisions[in.ID] = entry.ExpectedRevision, in.Revision
		incidents = append(incidents, in)
		ids = append(ids, in.ID)
	}
	sort.Strings(ids)
	rec := domain.BatchRequestRecord{RequestID: command.RequestID, Digest: digest, BatchID: command.RequestID, IncidentIDs: ids, Revisions: revisions, Results: incidents}
	if err := s.Repo.CommitBatch(incidents, expected, rec); err != nil {
		if err == domain.ErrConflict {
			current := s.PreflightBatchAssignment(command)
			return current, &domain.BatchConflictError{Results: current.Results}
		}
		return preflight, err
	}
	result := BatchAssignmentResult{BatchID: command.RequestID, Incidents: incidents}
	for _, in := range incidents {
		result.Results = append(result.Results, domain.BatchIncidentResult{IncidentID: in.ID, Valid: true, Status: in.Status, Revision: in.Revision})
	}
	sort.Slice(result.Results, func(a, b int) bool { return result.Results[a].IncidentID < result.Results[b].IncidentID })
	return result, nil
}

func (s *Service) ChangePlan(id string, revision int, change domain.PlanChange, reason, approver, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Change           domain.PlanChange
		Reason, Approver string
	}{change, reason, approver})
	if in, handled, err := s.reuse(requestID, "plan-change", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ChangePlan(revision, change, reason, approver, requestID, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "plan-change", digest)
}

func (s *Service) AddProcessRecord(id string, revision int, itemID string, record domain.ProcessRecord, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		ItemID string
		Record domain.ProcessRecord
		Actor  string
	}{itemID, record, actor})
	if in, handled, err := s.reuse(requestID, "process-record", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if record.Reading != nil {
		if record.Reading.ID == "" {
			record.Reading.ID = fmt.Sprintf("%s-process-%d", itemID, revision)
		}
		normalized, normalizeErr := assessment.Normalize(*record.Reading)
		if normalizeErr != nil {
			return nil, &domain.ValidationError{Field: "reading.unit", Message: normalizeErr.Error()}
		}
		record.Reading = &normalized
		for _, c := range in.RetestCheckpoints {
			if c.Status == "已完成" && c.ReadingID == record.Reading.ID {
				return nil, &domain.ValidationError{Field: "reading.id", Message: "同一读数不得完成多个检查点"}
			}
		}
	}
	if err = in.AddProcessRecord(revision, itemID, record, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	if record.Reading != nil {
		for n := range in.RetestCheckpoints {
			c := &in.RetestCheckpoints[n]
			if c.Status != "待复测" || c.ItemID != itemID || c.Metric != record.Reading.Metric {
				continue
			}
			if s.now().After(c.PlannedAt.Add(c.AllowedDeviation)) {
				c.Status = "已错过"
				c.MissReason = "读数超出允许时间偏差"
				continue
			}
			if !record.Reading.MeasuredAt.Before(c.PlannedAt.Add(-c.AllowedDeviation)) && !record.Reading.MeasuredAt.After(c.PlannedAt.Add(c.AllowedDeviation)) {
				c.Status, c.ReadingID, c.EvidenceRef = "已完成", record.Reading.ID, record.Reading.EvidenceRef
				t := s.now()
				c.CompletedAt = &t
				break
			}
		}
	}
	if record.Reading != nil && !assessment.WithinThreshold(record.Reading.Metric, record.Reading.Value, lockedRules(in, s.Rules)) {
		in.Escalation = &domain.ProcessEscalation{ItemID: itemID, Metrics: []string{record.Reading.Metric}, TriggerReadingIDs: []string{record.Reading.ID}, Reason: "过程记录读数重新越界", SuggestedRetestAt: s.now().Add(2 * time.Hour)}
		if in.Plan != nil {
			for n := range in.Plan.Items {
				if in.Plan.Items[n].ID == itemID {
					in.Plan.Items[n].Status = "需整改"
					in.Plan.Items[n].Executable = false
					t := s.now()
					in.Plan.Items[n].PausedAt = &t
				} else {
					for _, dep := range in.Plan.Items[n].PrerequisiteIDs {
						if dep == itemID && in.Plan.Items[n].Status != "已完成" {
							in.Plan.Items[n].BlockedBy = []string{itemID}
							in.Plan.Items[n].Executable = false
						}
					}
				}
			}
		}
		in.AppendEscalationEvent(actor, requestID, record.Reading.Metric, []string{record.Reading.ID})
	}
	return s.commit(in, revision, requestID, "process-record", digest)
}

func (s *Service) CompleteItemWithRecords(id string, revision int, itemID, note string, readings []domain.EnvironmentalReading, recordSequences []int, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		ItemID, Note, Actor string
		Readings            []domain.EnvironmentalReading
		Records             []int
	}{itemID, note, actor, readings, recordSequences})
	if in, handled, err := s.reuse(requestID, "item-completion", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ValidateCompletionRecords(itemID, recordSequences); err != nil {
		return nil, err
	}
	normalized, err := s.normalizeEffects(in, readings)
	if err != nil {
		return nil, err
	}
	if err = in.CompleteItemReadings(revision, itemID, note, normalized, recordSequences, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	for _, reading := range normalized {
		for n := range in.RetestCheckpoints {
			c := &in.RetestCheckpoints[n]
			if c.Status == "待复测" && c.ItemID == itemID && c.Metric == reading.Metric && !reading.MeasuredAt.Before(c.PlannedAt.Add(-c.AllowedDeviation)) && !reading.MeasuredAt.After(c.PlannedAt.Add(c.AllowedDeviation)) {
				c.Status, c.ReadingID, c.EvidenceRef = "已完成", reading.ID, reading.EvidenceRef
				t := s.now()
				c.CompletedAt = &t
				break
			}
		}
	}
	if len(in.Timeline) > 0 {
		in.Timeline[len(in.Timeline)-1].Payload["process_record_sequences"] = recordSequences
	}
	rules := lockedRules(in, s.Rules)
	in.Comparisons = assessment.Compare(in.Readings, rules)
	in.ApplyStability(currentRoundStability(in, rules))
	return s.commit(in, revision, requestID, "item-completion", digest)
}

func (s *Service) RequestDeadlineChange(id string, revision int, requested time.Time, reason string, affected []string, applicant, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Requested         time.Time
		Reason, Applicant string
		Affected          []string
	}{requested, reason, applicant, affected})
	if in, handled, err := s.reuse(requestID, "deadline-request", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.RequestDeadlineChange(revision, requested, reason, affected, applicant, requestID, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "deadline-request", digest)
}

func (s *Service) DecideDeadlineChange(id string, revision int, approve bool, decider, note, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Approve       bool
		Decider, Note string
	}{approve, decider, note})
	if in, handled, err := s.reuse(requestID, "deadline-decision", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.DecideDeadlineChange(revision, approve, decider, note, requestID, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "deadline-decision", digest)
}

func (s *Service) VerifyMetrics(id string, revision int, reviewer, overall, reason, requestID string, decisions []domain.MetricVerification) (*domain.PreservationIncident, error) {
	return s.VerifyMetricsWithStandards(id, revision, reviewer, overall, reason, requestID, decisions, nil)
}

func (s *Service) VerifyMetricsWithStandards(id string, revision int, reviewer, overall, reason, requestID string, decisions []domain.MetricVerification, standards []domain.AcceptanceStandard) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Reviewer, Overall, Reason string
		Decisions                 []domain.MetricVerification
		Standards                 []domain.AcceptanceStandard
	}{reviewer, overall, reason, decisions, standards})
	if in, handled, err := s.reuse(requestID, "metric-verification", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	failures, err := in.ValidateMetricVerification(decisions, overall)
	if err != nil {
		return nil, err
	}
	confirmed := []string{}
	for _, decision := range decisions {
		confirmed = append(confirmed, decision.ConfirmedReadingIDs...)
	}
	sort.Strings(confirmed)
	comparisons := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	now := s.now()
	if overall == "合格" && len(in.AcceptanceStandards) > 0 {
		if err = domain.ValidateAcceptanceResults(comparisons, in.AcceptanceStandards, in.Readings, now); err != nil {
			return nil, err
		}
	}
	if overall == "退回" {
		if err = domain.ValidateAcceptanceStandards(comparisons, failures, standards, now, in.RuleSnapshot); err != nil {
			return nil, err
		}
	}
	if err = in.VerifyConfirmedWithComparisonsAt(revision, reviewer, overall, reason, requestID, comparisons, confirmed, now); err != nil {
		return nil, err
	}
	in.Verification.MetricDecisions = append([]domain.MetricVerification(nil), decisions...)
	in.Rounds[in.Verification.Round-1].Verification = cloneVerificationForWorkflow(in.Verification)
	if overall == "退回" {
		in.AcceptanceStandards = append([]domain.AcceptanceStandard(nil), standards...)
		items := make([]domain.MitigationItem, 0, len(failures))
		for n, metric := range failures {
			items = append(items, domain.MitigationItem{ID: fmt.Sprintf("round-%d-item-%d", in.CurrentRound, n+1), Description: "整改" + metric + "异常", Status: "待执行", CoveredMetrics: []string{metric}})
		}
		in.Plan.Items = items
		in.RefreshPlanState()
		in.Rounds[len(in.Rounds)-1].Plan = *in.Plan
		in.Timeline[len(in.Timeline)-1].Payload["failed_metrics"] = failures
		in.Timeline[len(in.Timeline)-1].Payload["metric_decisions"] = decisions
	} else {
		in.Timeline[len(in.Timeline)-1].Payload["metric_decisions"] = decisions
		if err = in.FreezeArchive(now); err != nil {
			return nil, err
		}
	}
	return s.commit(in, revision, requestID, "metric-verification", digest)
}

func cloneVerificationForWorkflow(value *domain.Verification) *domain.Verification {
	if value == nil {
		return nil
	}
	cp := *value
	cp.MetricDecisions = append([]domain.MetricVerification(nil), value.MetricDecisions...)
	return &cp
}
