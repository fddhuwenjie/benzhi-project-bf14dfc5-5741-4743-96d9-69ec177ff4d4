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

func (i *PreservationIncident) SetRegistrationContext(candidates []IncidentCandidate, reason string) {
	i.RelatedCandidates = append([]IncidentCandidate(nil), candidates...)
	i.IndependentReason = strings.TrimSpace(reason)
	if len(i.Timeline) == 0 {
		return
	}
	i.Timeline[0].Payload["source_candidates"] = i.RelatedCandidates
	if i.IndependentReason != "" {
		i.Timeline[0].Payload["independent_reason"] = i.IndependentReason
	}
}

func validateDependencies(items []MitigationItem) error {
	byID := make(map[string]int, len(items))
	for n, item := range items {
		if _, exists := byID[item.ID]; exists {
			return &ValidationError{Field: fmt.Sprintf("items[%d].id", n), Message: "措施项编号不得重复"}
		}
		byID[item.ID] = n
	}
	for n, item := range items {
		seen := map[string]bool{}
		for _, dependency := range item.PrerequisiteIDs {
			if dependency == item.ID {
				return &ValidationError{Field: fmt.Sprintf("items[%d].prerequisite_ids", n), Message: "措施项不能依赖自身"}
			}
			if _, exists := byID[dependency]; !exists {
				return &ValidationError{Field: fmt.Sprintf("items[%d].prerequisite_ids", n), Message: "前置措施项不存在: " + dependency}
			}
			if seen[dependency] {
				return &ValidationError{Field: fmt.Sprintf("items[%d].prerequisite_ids", n), Message: "前置措施项编号不得重复"}
			}
			seen[dependency] = true
		}
	}
	state := make(map[string]int, len(items))
	var stack []string
	var visit func(string) error
	visit = func(id string) error {
		state[id] = 1
		stack = append(stack, id)
		for _, dependency := range items[byID[id]].PrerequisiteIDs {
			if state[dependency] == 1 {
				start := 0
				for start < len(stack) && stack[start] != dependency {
					start++
				}
				cycle := append(append([]string(nil), stack[start:]...), dependency)
				return &ValidationError{Field: "items.prerequisite_ids", Message: "措施依赖存在循环: " + strings.Join(cycle, " -> ")}
			}
			if state[dependency] == 0 {
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		return nil
	}
	for _, item := range items {
		if state[item.ID] == 0 {
			if err := visit(item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func incompletePrerequisites(items []MitigationItem, item MitigationItem) []string {
	status := make(map[string]string, len(items))
	for _, candidate := range items {
		status[candidate.ID] = candidate.Status
	}
	var blocked []string
	for _, dependency := range item.PrerequisiteIDs {
		if status[dependency] != "已完成" {
			blocked = append(blocked, dependency)
		}
	}
	sort.Strings(blocked)
	return blocked
}

func refreshPlanProgress(plan *MitigationPlan) {
	completed, active := 0, 0
	for n := range plan.Items {
		if plan.Items[n].Status == "已取消" || plan.Items[n].CancelledAt != nil {
			plan.Items[n].BlockedBy = nil
			plan.Items[n].Executable = false
			continue
		}
		active++
		plan.Items[n].BlockedBy = incompletePrerequisites(plan.Items, plan.Items[n])
		plan.Items[n].Executable = plan.Items[n].Status != "已完成" && len(plan.Items[n].BlockedBy) == 0 && plan.Items[n].PausedAt == nil
		if plan.Items[n].Status == "已完成" {
			completed++
		}
	}
	if active == 0 {
		plan.Progress = 0
		return
	}
	plan.Progress = float64(completed) / float64(active)
}

func (i *PreservationIncident) RefreshPlanState() {
	if i.Plan == nil {
		return
	}
	refreshPlanProgress(i.Plan)
	i.syncCurrentRound()
}

func (i *PreservationIncident) CorrectItemReadings(expected int, itemID, note, reason string, effects []EnvironmentalReading, actor, req string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" || utf8.RuneCountInString(strings.TrimSpace(reason)) > 1000 {
		return &ValidationError{Field: "correction_reason", Message: "更正原因不能为空且不得超过 1000 个字符"}
	}
	idx := -1
	for n := range i.Plan.Items {
		if i.Plan.Items[n].ID == itemID {
			idx = n
			break
		}
	}
	if idx < 0 || i.Plan.Items[idx].Status != "已完成" {
		return &ValidationError{Field: "item_id", Message: "只能更正当前轮次已完成的措施项"}
	}
	if i.Plan.Items[idx].CorrectionCount > 0 {
		return &ValidationError{Field: "item_id", Message: "每个措施项只允许更正一次"}
	}
	if strings.TrimSpace(note) == "" || utf8.RuneCountInString(note) > 2000 {
		return &ValidationError{Field: "note", Message: "措施说明不能为空且不得超过 2000 个字符"}
	}
	if len(effects) == 0 {
		return &ValidationError{Field: "effect_readings", Message: "至少需要一条完整效果读数"}
	}
	oldIDs := append([]string(nil), i.Plan.Items[idx].EffectReadingIDs...)
	assignmentAt := i.Rounds[len(i.Rounds)-1].StartedAt
	seenIDs, seenEvidence := map[string]bool{}, map[string]bool{}
	for _, existing := range i.Readings {
		seenIDs[existing.ID] = true
		if existing.ReplacedByID == "" {
			seenEvidence[strings.TrimSpace(existing.EvidenceRef)] = true
		}
	}
	newIDs := make([]string, len(effects))
	for n := range effects {
		r := &effects[n]
		if r.ID == "" {
			r.ID = fmt.Sprintf("%s-r%d-%s-correction-%d", i.ID, i.CurrentRound, itemID, n+1)
		}
		if seenIDs[r.ID] {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].id", n), Message: "效果读数标识重复"}
		}
		seenIDs[r.ID] = true
		if r.MeasuredAt.Before(assignmentAt) || r.MeasuredAt.After(now) {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].measured_at", n), Message: "效果测量时间必须介于本轮分派与提交之间"}
		}
		if strings.TrimSpace(r.SourceNote) == "" || utf8.RuneCountInString(r.SourceNote) > 500 {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].source_note", n), Message: "来源说明不能为空且不得超过 500 个字符"}
		}
		ref := strings.TrimSpace(r.EvidenceRef)
		if ref == "" || utf8.RuneCountInString(ref) > 500 || seenEvidence[ref] {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].evidence_ref", n), Message: "证据引用不能为空、超长或复用于其他有效读数"}
		}
		seenEvidence[ref] = true
		if r.EvidenceRecordedAt.Before(r.MeasuredAt) || r.EvidenceRecordedAt.After(now) {
			return &ValidationError{Field: fmt.Sprintf("effect_readings[%d].evidence_recorded_at", n), Message: "证据时间必须介于效果测量与提交之间"}
		}
		r.IncidentID, r.Phase, r.EvidenceRef = i.ID, PhaseEffect, ref
		if n < len(oldIDs) {
			r.ReplacesReadingID = oldIDs[n]
		}
		newIDs[n] = r.ID
	}
	for n := range i.Readings {
		for oldIndex, oldID := range oldIDs {
			if i.Readings[n].ID == oldID {
				replacement := newIDs[len(newIDs)-1]
				if oldIndex < len(newIDs) {
					replacement = newIDs[oldIndex]
				}
				i.Readings[n].ReplacedByID = replacement
			}
		}
	}
	i.Readings = append(i.Readings, effects...)
	i.Plan.Items[idx].Note = strings.TrimSpace(note)
	i.Plan.Items[idx].EffectReadingIDs = newIDs
	i.Plan.Items[idx].Evidence = effects[0].EvidenceRef
	i.Plan.Items[idx].CompletedAt = ptrTime(now)
	i.Plan.Items[idx].CorrectionCount++
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("执行记录更正", actor, req, map[string]interface{}{"item_id": itemID, "old_reading_ids": oldIDs, "new_reading_ids": newIDs, "reason": strings.TrimSpace(reason)})
	refreshPlanProgress(i.Plan)
	i.syncCurrentRound()
	return nil
}

func (i *PreservationIncident) checkVerificationResponsibility(reviewer string, now time.Time) (ResponsibilityCheck, error) {
	recorders := map[string]bool{}
	assignor := ""
	for _, event := range i.Timeline {
		if event.Round == i.CurrentRound && event.EventType == "分派" {
			assignor = event.Actor
		}
		if event.Round == i.CurrentRound && (event.EventType == "措施完成" || event.EventType == "执行记录更正") {
			recorders[event.Actor] = true
		}
	}
	people := make([]string, 0, len(recorders))
	for actor := range recorders {
		people = append(people, actor)
	}
	sort.Strings(people)
	check := ResponsibilityCheck{Reviewer: reviewer, Assignor: assignor, Assignee: i.Assignee, Recorders: people, Separated: reviewer != i.Assignee && !recorders[reviewer], CheckedAt: now}
	if !check.Separated {
		return check, &ValidationError{Field: "reviewer", Message: "复核人不得是当前轮次执行人或措施记录人"}
	}
	return check, nil
}

func (i *PreservationIncident) validateReviewEvidence(comparisons []ReadingComparison, confirmedIDs []string) error {
	missing := []string{}
	validReading := map[string]EnvironmentalReading{}
	for _, reading := range i.Readings {
		if reading.ReplacedByID == "" {
			validReading[reading.ID] = reading
		}
	}
	seenEvidence := map[string]bool{}
	for _, item := range i.Plan.Items {
		if strings.TrimSpace(item.Note) == "" {
			missing = append(missing, item.ID+":说明")
		}
		for _, id := range item.EffectReadingIDs {
			reading, ok := validReading[id]
			if !ok || strings.TrimSpace(reading.EvidenceRef) == "" || seenEvidence[reading.EvidenceRef] {
				missing = append(missing, item.ID+":"+id)
				continue
			}
			seenEvidence[reading.EvidenceRef] = true
		}
	}
	if len(missing) > 0 {
		return &ValidationError{Field: "verification_evidence", Message: "当前轮次措施说明或有效证据不完整", MissingMetrics: missing, Comparisons: comparisons}
	}
	expected := comparisonIDs(comparisons)
	sort.Strings(expected)
	actual := append([]string(nil), confirmedIDs...)
	sort.Strings(actual)
	if len(expected) != len(actual) {
		return &ValidationError{Field: "confirmed_reading_ids", Message: "确认读数集合与当前有效比较集不一致", Comparisons: comparisons}
	}
	for n := range expected {
		if expected[n] != actual[n] || n > 0 && actual[n] == actual[n-1] {
			return &ValidationError{Field: "confirmed_reading_ids", Message: "确认读数集合与当前有效比较集不一致", Comparisons: comparisons}
		}
	}
	return nil
}

func (i *PreservationIncident) FreezeArchive(closedAt time.Time) error {
	if i.Status != StatusClosed || i.Archive != nil {
		if i.Archive != nil {
			return nil
		}
		return ErrState
	}
	archive := &ArchiveSummary{Version: "1", ChecksumStatus: "有效", IncidentID: i.ID, AreaID: i.AreaID, AffectedScope: i.AffectedScope, Sensitivity: i.Sensitivity, RiskLevel: i.RiskLevel, RiskBasis: append([]string(nil), i.RiskBasis...), ObservedAt: i.ObservedAt, CreatedAt: i.CreatedAt, ClosedAt: closedAt, AffectedItems: append([]AffectedCollectionItem(nil), i.AffectedItems...)}
	participants, evidence := map[string]bool{}, map[string]bool{}
	for _, event := range i.Timeline {
		if event.Actor != "" {
			participants[event.Actor] = true
		}
		if event.EventType == "分派" && archive.AssignedAt == nil {
			at := event.OccurredAt
			archive.AssignedAt = &at
		}
	}
	if archive.AssignedAt != nil {
		archive.ResponseOverdue = archive.AssignedAt.After(i.ObservedAt.Add(i.ResponseDue))
	}
	for _, round := range i.Rounds {
		archive.Rounds = append(archive.Rounds, ArchiveRoundSummary{Round: round.Number, PlanID: round.Plan.ID, Assignee: round.Plan.Owner, DueAt: round.Plan.DueAt, Items: clonePlan(round.Plan).Items, Verification: cloneVerification(round.Verification), ReturnedReason: round.ReturnedReason})
		if round.Plan.OverdueNote != "" {
			archive.OverdueNotes = append(archive.OverdueNotes, round.Plan.OverdueNote)
		}
		if !round.Plan.DueAt.IsZero() && closedAt.After(round.Plan.DueAt) {
			archive.TreatmentOverdue = true
		}
		if round.Plan.Owner != "" {
			participants[round.Plan.Owner] = true
		}
	}
	current := map[string]bool{}
	if i.Plan != nil {
		for _, item := range i.Plan.Items {
			for _, id := range item.EffectReadingIDs {
				current[id] = true
			}
		}
	}
	for _, reading := range i.Readings {
		if reading.EvidenceRef != "" {
			evidence[reading.EvidenceRef] = true
		}
		if reading.ReplacedByID == "" && (reading.Phase != PhaseEffect || current[reading.ID]) {
			archive.FinalReadings = append(archive.FinalReadings, reading)
		}
	}
	for value := range evidence {
		archive.EvidenceRefs = append(archive.EvidenceRefs, value)
	}
	for value := range participants {
		archive.Participants = append(archive.Participants, value)
	}
	sort.Strings(archive.EvidenceRefs)
	sort.Strings(archive.Participants)
	archive.Checksum = archiveChecksum(archive)
	i.Archive = archive
	return nil
}

func (i *PreservationIncident) VerifyArchive() {
	if i.Archive == nil {
		return
	}
	if archiveChecksum(i.Archive) == i.Archive.Checksum {
		i.Archive.ChecksumStatus = "有效"
	} else {
		i.Archive.ChecksumStatus = "不一致"
	}
}

func archiveChecksum(archive *ArchiveSummary) string {
	cp := *archive
	cp.Checksum, cp.ChecksumStatus = "", ""
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func eventStates(eventType string, current Status) (Status, Status) {
	switch eventType {
	case "登记与研判":
		return "", StatusPending
	case "分派":
		return StatusPending, StatusMitigating
	case "提交复核":
		return StatusMitigating, StatusReview
	case "关闭":
		return StatusReview, StatusClosed
	case "退回处置":
		return StatusReview, StatusMitigating
	case "执行人交接", "措施批量完成", "登记读数更正", "补充观测", "风险变更", "方案变更", "措施过程记录", "期限变更申请", "期限变更决定":
		return current, current
	default:
		return current, current
	}
}

func eventObjectID(eventType string, payload map[string]interface{}) string {
	keys := []string{"item_id", "verification_id", "plan_id"}
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			return value
		}
	}
	return ""
}

func DecorateTimeline(events []IncidentEvent) []IncidentEvent {
	result := append([]IncidentEvent(nil), events...)
	status := Status("")
	revision := 0
	for n := range result {
		beforeStatus, afterStatus := eventStates(result[n].EventType, status)
		if result[n].StatusAfter == "" {
			result[n].StatusBefore, result[n].StatusAfter = beforeStatus, afterStatus
		}
		result[n].RevisionBefore, result[n].RevisionAfter = revision, revision+1
		status, revision = result[n].StatusAfter, revision+1
		if result[n].ObjectID == "" {
			result[n].ObjectID = eventObjectID(result[n].EventType, result[n].Payload)
		}
	}
	return result
}
