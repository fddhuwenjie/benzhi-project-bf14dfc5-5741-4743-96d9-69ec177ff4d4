package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type RuleDiff struct {
	Metric      string `json:"metric"`
	OldBoundary string `json:"old_boundary"`
	NewBoundary string `json:"new_boundary"`
	OldMatched  bool   `json:"old_matched"`
	NewMatched  bool   `json:"new_matched"`
	Reason      string `json:"reason,omitempty"`
}
type ReassessmentPreview struct {
	IncidentID            string        `json:"incident_id"`
	Revision              int           `json:"revision"`
	TemplateVersion       string        `json:"template_version"`
	Checksum              string        `json:"preview_checksum"`
	OldRuleSnapshot       RuleSnapshot  `json:"old_rule_snapshot"`
	CandidateRuleSnapshot RuleSnapshot  `json:"candidate_rule_snapshot"`
	OldRiskLevel          RiskLevel     `json:"old_risk_level"`
	CandidateRiskLevel    RiskLevel     `json:"candidate_risk_level"`
	OldRiskBasis          []string      `json:"old_risk_basis"`
	CandidateRiskBasis    []string      `json:"candidate_risk_basis"`
	CandidateHits         []RuleHit     `json:"candidate_rule_hits"`
	RuleDiffs             []RuleDiff    `json:"rule_diffs"`
	AddedHits             []RuleHit     `json:"added_hits,omitempty"`
	RemovedHits           []RuleHit     `json:"removed_hits,omitempty"`
	MissingBaselines      []string      `json:"missing_baselines,omitempty"`
	ResponseDue           time.Duration `json:"response_due"`
	CreatedAt             time.Time     `json:"created_at"`
}

func ReassessmentChecksum(id string, revision int, template string, result ReassessmentPreview) string {
	b, _ := json.Marshal(struct {
		ID        string
		Revision  int
		Template  string
		Candidate RiskLevel
		Diffs     []RuleDiff
		Hits      []RuleHit
		Missing   []string
	}{id, revision, template, result.CandidateRiskLevel, result.RuleDiffs, result.CandidateHits, result.MissingBaselines})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (i *PreservationIncident) ReplaceAssessment(expected int, preview ReassessmentPreview, actor, requestID, explanation string, now time.Time) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if preview.Checksum == "" {
		return &ValidationError{Field: "preview_checksum", Message: "预览校验值不能为空"}
	}
	if riskRank(preview.CandidateRiskLevel) < riskRank(i.RiskLevel) && strings.TrimSpace(explanation) == "" {
		return &ValidationError{Field: "risk_level", Message: "风险降级必须进入人工复核并提交明确说明"}
	}
	i.RiskLevel = preview.CandidateRiskLevel
	i.RiskBasis = append([]string(nil), preview.CandidateRiskBasis...)
	i.ResponseDue = preview.ResponseDue
	i.RuleSetVersion = preview.TemplateVersion
	i.RuleSnapshot = preview.CandidateRuleSnapshot
	i.RuleHits = append([]RuleHit(nil), preview.CandidateHits...)
	i.MissingBaselines = append([]string(nil), preview.MissingBaselines...)
	i.Revision++
	if riskRank(preview.CandidateRiskLevel) < riskRank(preview.OldRiskLevel) {
		i.PendingManualReview = true
		i.ManualReviewMissing = []string{"规则变更风险降级人工复核"}
		i.AppendManualReviewEvent(actor, requestID, i.ManualReviewMissing)
	}
	i.UpdatedAt = now
	i.RefreshDeadline(now)
	i.appendEvent("研判规则变更", actor, requestID, map[string]interface{}{"old_rule_version": preview.OldRuleSnapshot.Version, "new_rule_version": preview.TemplateVersion, "old_risk_level": preview.OldRiskLevel, "new_risk_level": preview.CandidateRiskLevel, "response_due": preview.ResponseDue.String(), "preview_checksum": preview.Checksum})
	return nil
}

// RuleDiffsToHits is intentionally conservative: callers may provide full hits via payload;
// this returns the candidate hit projection encoded in the diff list.

func (i *PreservationIncident) InvalidateReadings(expected int, ids []string, reason, evidence, actor, requestID string, now time.Time, level RiskLevel, basis []string, response time.Duration, intervals []AbnormalInterval, pairings []BaselinePairing, missing []string, hits []RuleHit) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" || utf8.RuneCountInString(strings.TrimSpace(reason)) > 1000 {
		return &ValidationError{Field: "reason", Message: "失效原因不能为空且不得超过 1000 个字符"}
	}
	if len(ids) == 0 {
		return &ValidationError{Field: "reading_ids", Message: "至少选择一条读数"}
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			return &ValidationError{Field: "reading_ids", Message: "读数标识不得重复"}
		}
		selected[id] = true
	}
	metrics := map[string]int{}
	active := map[string]int{}
	for _, r := range i.Readings {
		if r.ReplacedByID == "" && (r.Phase == PhaseBaseline || r.Phase == PhaseAbnormal) {
			active[r.Metric]++
			if r.Phase == PhaseAbnormal {
				metrics[r.Metric]++
			}
		}
	}
	for _, r := range i.Readings {
		if !selected[r.ID] {
			continue
		}
		if r.ReplacedByID != "" || (r.Phase != PhaseBaseline && r.Phase != PhaseAbnormal) {
			return &ValidationError{Field: "reading_ids", Message: "存在已失效或已替代读数"}
		}
		if r.Phase == PhaseAbnormal && metrics[r.Metric] <= 1 {
			return &ValidationError{Field: "reading_ids", Message: "撤回后指标缺少可比较异常读数"}
		}
		if r.Phase == PhaseBaseline && active[r.Metric] <= 1 {
			return &ValidationError{Field: "reading_ids", Message: "撤回后指标缺少基线读数"}
		}
	}
	for n := range i.Readings {
		if selected[i.Readings[n].ID] {
			i.Readings[n].ReplacedByID = "invalidated:" + requestID
		}
	}
	i.RiskLevel = level
	i.RiskBasis = append([]string(nil), basis...)
	i.ResponseDue = response
	i.AssessmentIntervals = append([]AbnormalInterval(nil), intervals...)
	i.BaselinePairings = append([]BaselinePairing(nil), pairings...)
	i.MissingBaselines = append([]string(nil), missing...)
	i.RuleHits = append([]RuleHit(nil), hits...)
	i.Revision++
	i.UpdatedAt = now
	i.RefreshDeadline(now)
	i.appendEvent("登记证据失效", actor, requestID, map[string]interface{}{"reading_ids": ids, "reason": strings.TrimSpace(reason), "evidence_ref": strings.TrimSpace(evidence), "risk_level": level})
	return nil
}

func (i *PreservationIncident) SetReviewLock(lock ReviewLock) { i.ReviewLock = &lock }

func (i *PreservationIncident) EscalateProcess(expected int, itemID string, metrics, readingIDs []string, reason string, actor, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(reason) == "" {
		return &ValidationError{Field: "reason", Message: "升级说明不能为空"}
	}
	found := false
	for n := range i.Plan.Items {
		if i.Plan.Items[n].ID == itemID {
			i.Plan.Items[n].Status = "需整改"
			i.Plan.Items[n].Executable = false
			i.Plan.Items[n].PauseReason = reason
			i.Plan.Items[n].PausedAt = &now
			found = true
		} else {
			for _, dep := range i.Plan.Items[n].PrerequisiteIDs {
				if dep == itemID && i.Plan.Items[n].Status != "已完成" {
					i.Plan.Items[n].BlockedBy = []string{itemID}
					i.Plan.Items[n].Executable = false
				}
			}
		}
	}
	if !found {
		return &ValidationError{Field: "item_id", Message: "措施项不存在"}
	}
	i.Escalation = &ProcessEscalation{ItemID: itemID, Metrics: append([]string(nil), metrics...), TriggerReadingIDs: append([]string(nil), readingIDs...), Reason: reason, SuggestedRetestAt: now.Add(2 * time.Hour)}
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("执行异常升级", actor, requestID, map[string]interface{}{"item_id": itemID, "metrics": metrics, "trigger_reading_ids": readingIDs, "reason": reason})
	return nil
}

func (i *PreservationIncident) ResolveEscalation(expected int, note, actor, requestID string, now time.Time) error {
	if i.Escalation == nil {
		return &ValidationError{Field: "escalation", Message: "当前没有待处理升级"}
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if strings.TrimSpace(note) == "" {
		return &ValidationError{Field: "correction_note", Message: "整改说明不能为空"}
	}
	for n := range i.Plan.Items {
		if i.Plan.Items[n].ID == i.Escalation.ItemID {
			i.Plan.Items[n].PausedAt = nil
			i.Plan.Items[n].PauseReason = ""
			i.Plan.Items[n].Status = "待执行"
		}
	}
	item := i.Escalation.ItemID
	i.Escalation.CorrectionNote = note
	i.Escalation.Confirmed = true
	i.Escalation = nil
	i.RefreshPlanState()
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("执行异常恢复", actor, requestID, map[string]interface{}{"item_id": item, "correction_note": note})
	return nil
}

func (i *PreservationIncident) AppendEscalationEvent(actor, requestID, metric string, ids []string) {
	i.appendEvent("执行异常升级", actor, requestID, map[string]interface{}{"metric": metric, "trigger_reading_ids": ids, "reason": "效果读数重新越界"})
}

func SortedStrings(v []string) []string {
	out := append([]string(nil), v...)
	sort.Strings(out)
	return out
}
