package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type ItemCompletion struct {
	ID             string                 `json:"id,omitempty"`
	ItemID         string                 `json:"item_id"`
	Note           string                 `json:"note"`
	Evidence       string                 `json:"evidence,omitempty"`
	EffectReading  *EnvironmentalReading  `json:"effect_reading,omitempty"`
	EffectReadings []EnvironmentalReading `json:"effect_readings"`
}

func (i *PreservationIncident) SetAssessmentSnapshot(pairings []BaselinePairing, missing []string, version string, hits []RuleHit, snapshot RuleSnapshot) {
	i.BaselinePairings = append([]BaselinePairing{}, pairings...)
	i.MissingBaselines = append([]string{}, missing...)
	i.RuleSetVersion = version
	i.RuleHits = append([]RuleHit{}, hits...)
	i.RuleSnapshot = snapshot
	if len(i.Timeline) > 0 {
		i.Timeline[0].Payload["baseline_pairings"] = i.BaselinePairings
		i.Timeline[0].Payload["missing_baseline_metrics"] = i.MissingBaselines
		i.Timeline[0].Payload["rule_set_version"] = i.RuleSetVersion
		i.Timeline[0].Payload["rule_hits"] = i.RuleHits
		i.Timeline[0].Payload["response_due"] = i.ResponseDue.String()
	}
}

func (i *PreservationIncident) CorrectRegistrationReading(expected int, readingID string, replacement EnvironmentalReading, reason, actor, requestID string, now time.Time, level RiskLevel, basis []string, response time.Duration, intervals []AbnormalInterval, pairings []BaselinePairing, missing []string, hits []RuleHit) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" || utf8.RuneCountInString(strings.TrimSpace(reason)) > 1000 {
		return &ValidationError{Field: "reason", Message: "更正原因不能为空且不得超过 1000 个字符"}
	}
	index := -1
	for n, reading := range i.Readings {
		if reading.ID == readingID && reading.ReplacedByID == "" && (reading.Phase == PhaseBaseline || reading.Phase == PhaseAbnormal) {
			index = n
			break
		}
	}
	if index < 0 {
		return &ValidationError{Field: "reading_id", Message: "未找到可更正的有效登记读数"}
	}
	if replacement.ID == "" {
		replacement.ID = fmt.Sprintf("%s-correction-%d", readingID, i.Revision)
	}
	for _, reading := range i.Readings {
		if reading.ID == replacement.ID {
			return &ValidationError{Field: "replacement_reading.id", Message: "替换读数标识已存在"}
		}
	}
	original := i.Readings[index]
	replacement.IncidentID = i.ID
	replacement.ReplacesReadingID = original.ID
	i.Readings[index].ReplacedByID = replacement.ID
	i.Readings = append(i.Readings, replacement)
	i.Evidence = append(i.Evidence, EvidenceSummary{ReadingID: replacement.ID, Metric: replacement.Metric, Reference: replacement.EvidenceRef, SourceNote: replacement.SourceNote, RecordedAt: replacement.EvidenceRecordedAt})
	i.RiskLevel = level
	i.RiskBasis = append([]string(nil), basis...)
	i.ResponseDue = response
	i.AssessmentIntervals = append([]AbnormalInterval(nil), intervals...)
	i.BaselinePairings = append([]BaselinePairing{}, pairings...)
	i.MissingBaselines = append([]string{}, missing...)
	i.RuleHits = append([]RuleHit{}, hits...)
	i.Revision++
	i.UpdatedAt = now
	i.RefreshDeadline(now)
	i.appendEvent("登记读数更正", actor, requestID, map[string]interface{}{"reading_id": readingID, "replacement_reading_id": replacement.ID, "reason": strings.TrimSpace(reason), "risk_level": level, "risk_basis": basis, "response_due": response.String(), "baseline_pairings": pairings, "rule_set_version": i.RuleSetVersion, "rule_hits": hits})
	return nil
}

func (i *PreservationIncident) TransferAssignee(expected int, assignee, reason string, due time.Time, actor, requestID string, workload WorkloadSnapshot, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(assignee) == "" {
		return &ValidationError{Field: "transfer_assignee", Message: "新执行人不能为空"}
	}
	if strings.TrimSpace(assignee) == i.Assignee {
		return &ValidationError{Field: "transfer_assignee", Message: "新执行人必须与当前执行人不同"}
	}
	if strings.TrimSpace(reason) == "" || utf8.RuneCountInString(strings.TrimSpace(reason)) > 1000 {
		return &ValidationError{Field: "transfer_reason", Message: "交接原因不能为空且不得超过 1000 个字符"}
	}
	if due.IsZero() || due.Before(now) {
		return &ValidationError{Field: "due_at", Message: "交接后的期限不得早于当前时间"}
	}
	for n, item := range i.Plan.Items {
		if item.CompletedAt != nil && due.Before(*item.CompletedAt) {
			return &ValidationError{Field: fmt.Sprintf("items[%d]", n), Message: "新期限与已完成措施时间不兼容"}
		}
	}
	transfer := AssigneeTransfer{FromAssignee: i.Assignee, ToAssignee: strings.TrimSpace(assignee), Reason: strings.TrimSpace(reason), PreviousDueAt: i.DueAt, NewDueAt: due, TransferredAt: now, Actor: actor}
	i.Assignee = transfer.ToAssignee
	i.DueAt = due
	i.Plan.Owner = transfer.ToAssignee
	i.Plan.DueAt = due
	i.Plan.Workload = workload
	i.AssigneeTransfers = append(i.AssigneeTransfers, transfer)
	i.Revision++
	i.UpdatedAt = now
	i.syncCurrentRound()
	i.appendEvent("执行人交接", actor, requestID, map[string]interface{}{"from_assignee": transfer.FromAssignee, "to_assignee": transfer.ToAssignee, "transfer_reason": transfer.Reason, "previous_due_at": transfer.PreviousDueAt, "due_at": transfer.NewDueAt, "completion_ratio": i.Plan.Progress, "workload_snapshot": workload, "overdue": now.After(due)})
	return nil
}

func (i *PreservationIncident) RecordItemsBatch(expected int, completions []ItemCompletion, actor, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if len(completions) == 0 {
		return &ValidationError{Field: "items", Message: "至少需要提交一个措施项"}
	}
	seen := map[string]bool{}
	for n := range completions {
		if completions[n].ItemID == "" {
			completions[n].ItemID = completions[n].ID
		}
		completion := completions[n]
		if strings.TrimSpace(completion.ItemID) == "" {
			return &ValidationError{Field: fmt.Sprintf("items[%d].item_id", n), Message: "措施项标识不能为空"}
		}
		if seen[completion.ItemID] {
			return &ValidationError{Field: fmt.Sprintf("items[%d].item_id", n), Message: "同一批次的措施项标识不得重复"}
		}
		seen[completion.ItemID] = true
	}
	temp := cloneIncident(i)
	baseEvents := len(temp.Timeline)
	for _, completion := range completions {
		if err := temp.RecordItemReadings(temp.Revision, completion.ItemID, completion.Note, completion.EffectReadings, actor, requestID, now); err != nil {
			return err
		}
	}
	temp.Timeline = temp.Timeline[:baseEvents]
	temp.Revision = expected + 1
	temp.UpdatedAt = now
	itemIDs := make([]string, 0, len(completions))
	readingIDs := make([]string, 0)
	for _, completion := range completions {
		itemIDs = append(itemIDs, completion.ItemID)
		for _, reading := range completion.EffectReadings {
			readingIDs = append(readingIDs, reading.ID)
		}
	}
	temp.appendEvent("措施批量完成", actor, requestID, map[string]interface{}{"item_ids": itemIDs, "effect_reading_ids": readingIDs, "completion_ratio": temp.Plan.Progress})
	*i = *temp
	return nil
}

func (i *PreservationIncident) ApplyStability(summaries []StabilitySummary) {
	i.Stability = append([]StabilitySummary(nil), summaries...)
	i.RetestMetrics = i.RetestMetrics[:0]
	byReading := map[string]StabilitySummary{}
	retest := map[string]bool{}
	for _, summary := range summaries {
		if !summary.Stable && len(summary.Trend) > 1 && !retest[summary.Metric] {
			i.RetestMetrics = append(i.RetestMetrics, summary.Metric)
			retest[summary.Metric] = true
		}
		for _, id := range summary.ParticipatingReadings {
			byReading[id] = summary
		}
	}
	sort.Strings(i.RetestMetrics)
	if i.Plan != nil {
		for n := range i.Plan.Items {
			seen := map[string]bool{}
			i.Plan.Items[n].Stability = nil
			for _, id := range i.Plan.Items[n].EffectReadingIDs {
				if summary, ok := byReading[id]; ok && !seen[summary.Metric] {
					i.Plan.Items[n].Stability = append(i.Plan.Items[n].Stability, summary)
					seen[summary.Metric] = true
				}
			}
		}
		i.syncCurrentRound()
	}
	if len(i.Timeline) > 0 {
		last := &i.Timeline[len(i.Timeline)-1]
		if last.EventType == "措施完成" || last.EventType == "措施批量完成" || last.EventType == "执行记录更正" {
			last.Payload["stability"] = summaries
			last.Payload["retest_metrics"] = i.RetestMetrics
		}
	}
}
