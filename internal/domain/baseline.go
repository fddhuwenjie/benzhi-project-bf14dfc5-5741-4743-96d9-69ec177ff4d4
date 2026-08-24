package domain

import (
	"fmt"
	"strings"
	"time"
)

func (i *PreservationIncident) AddBaselineReadings(expected int, readings []EnvironmentalReading, actor, requestID string, now time.Time, level RiskLevel, basis []string, response time.Duration, intervals []AbnormalInterval, pairings []BaselinePairing, missing []string, hits []RuleHit) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if len(readings) == 0 {
		return &ValidationError{Field: "readings", Message: "至少需要一条 baseline 补录读数"}
	}
	ids, refs := map[string]bool{}, map[string]bool{}
	for _, existing := range i.Readings {
		ids[existing.ID] = true
		refs[strings.TrimSpace(existing.EvidenceRef)] = true
	}
	for n := range readings {
		reading := &readings[n]
		if reading.Phase != PhaseBaseline {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].phase", n), Message: "基线补录读数的 phase 必须为 baseline"}
		}
		if ids[reading.ID] {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].id", n), Message: "读数标识已存在"}
		}
		ref := strings.TrimSpace(reading.EvidenceRef)
		if refs[ref] {
			return &ValidationError{Field: fmt.Sprintf("readings[%d].evidence_ref", n), Message: "证据引用已存在"}
		}
		ids[reading.ID], refs[ref] = true, true
		reading.IncidentID = i.ID
	}
	i.Readings = append(i.Readings, readings...)
	for _, reading := range readings {
		i.Evidence = append(i.Evidence, EvidenceSummary{ReadingID: reading.ID, Metric: reading.Metric, Reference: reading.EvidenceRef, SourceNote: reading.SourceNote, RecordedAt: reading.EvidenceRecordedAt})
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
	i.appendEvent("基线补录", actor, requestID, map[string]interface{}{
		"readings": readings, "risk_level": level, "risk_basis": basis,
		"response_due": response.String(), "baseline_pairings": pairings,
		"missing_baseline_metrics": missing, "rule_set_version": i.RuleSetVersion,
		"rule_hits": hits,
	})
	return nil
}
