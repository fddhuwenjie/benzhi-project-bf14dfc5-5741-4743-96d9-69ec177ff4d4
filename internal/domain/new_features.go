package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// AddAffectedItems appends a supplemental list without changing the frozen original entries.
func (i *PreservationIncident) AddAffectedItems(expected int, items []AffectedCollectionItem, observation SupplementalObservation, actor, requestID string, now time.Time) error {
	if i.Status != StatusPending {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	if len(items) == 0 {
		return &ValidationError{Field: "affected_items", Message: "至少需要一项新增藏品"}
	}
	if strings.TrimSpace(observation.Note) == "" {
		return &ValidationError{Field: "observation.note", Message: "发现说明不能为空"}
	}
	seen := map[string]bool{}
	for _, old := range i.AffectedItems {
		seen[old.CollectionID] = true
	}
	for n := range items {
		validated, _, _, err := ValidateAffectedItems([]AffectedCollectionItem{items[n]}, items[n].Sensitivity)
		if err != nil {
			return err
		}
		items[n] = validated[0]
		if seen[items[n].CollectionID] {
			return &ValidationError{Field: fmt.Sprintf("affected_items[%d].collection_id", n), Message: "藏品编号已存在于原清单或新增清单"}
		}
		seen[items[n].CollectionID] = true
	}
	evidenceRefs := map[string]bool{}
	for _, old := range i.Readings {
		if old.ReplacedByID == "" {
			evidenceRefs[old.EvidenceRef] = true
		}
	}
	for _, r := range observation.Readings {
		if strings.TrimSpace(r.EvidenceRef) == "" {
			return &ValidationError{Field: "observation.readings.evidence_ref", Message: "新增现场证据引用不能为空"}
		}
		if evidenceRefs[r.EvidenceRef] {
			return &ValidationError{Field: "observation.readings.evidence_ref", Message: "证据引用不得复用"}
		}
		evidenceRefs[r.EvidenceRef] = true
	}
	i.AffectedItems = append(i.AffectedItems, items...)
	triggers := map[string]bool{}
	highest := "低"
	for _, item := range i.AffectedItems {
		if sensitivityRank(item.Sensitivity) > sensitivityRank(highest) {
			highest = item.Sensitivity
		}
	}
	for _, item := range i.AffectedItems {
		if item.Sensitivity == highest {
			triggers[item.CollectionID] = true
		}
	}
	i.Sensitivity, i.SensitivityTriggers = highest, i.SensitivityTriggers[:0]
	for id := range triggers {
		i.SensitivityTriggers = append(i.SensitivityTriggers, id)
	}
	sort.Strings(i.SensitivityTriggers)
	total := 0
	for _, item := range i.AffectedItems {
		total += item.Quantity
	}
	i.AffectedScope = fmt.Sprintf("%d 项藏品，共 %d 件（最高敏感级别%s：%s）", len(i.AffectedItems), total, highest, strings.Join(i.SensitivityTriggers, "、"))
	observation.Sequence = len(i.AdditionalObservations) + 1
	observation.Actor, observation.RequestID = actor, requestID
	observation.ObservedAt = observation.ObservedAt.UTC()
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = now
	}
	i.AdditionalObservations = append(i.AdditionalObservations, observation)
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("受影响藏品范围增补", actor, requestID, map[string]interface{}{"added_items": items, "observation": observation, "affected_scope": i.AffectedScope, "sensitivity": i.Sensitivity})
	return nil
}

func (i *PreservationIncident) SetRetestCheckpoints(expected int, checkpoints []RetestCheckpoint, actor, requestID string, now time.Time) error {
	if i.Status != StatusMitigating || i.Plan == nil {
		return ErrState
	}
	if i.Revision != expected {
		return ErrConflict
	}
	seen := map[string]bool{}
	last := time.Time{}
	for n := range checkpoints {
		c := &checkpoints[n]
		if c.ID == "" {
			return &ValidationError{Field: fmt.Sprintf("checkpoints[%d].id", n), Message: "检查点编号不能为空"}
		}
		if seen[c.ID] {
			return &ValidationError{Field: fmt.Sprintf("checkpoints[%d].id", n), Message: "检查点编号不得重复"}
		}
		seen[c.ID] = true
		found := false
		metrics := map[string]bool{}
		for _, item := range i.Plan.Items {
			if item.ID == c.ItemID {
				found = true
				for _, m := range item.CoveredMetrics {
					metrics[m] = true
				}
			}
		}
		if !found || !metrics[c.Metric] {
			return &ValidationError{Field: fmt.Sprintf("checkpoints[%d].metric", n), Message: "检查点指标必须由措施覆盖"}
		}
		if c.PlannedAt.Before(i.Rounds[len(i.Rounds)-1].StartedAt) || c.PlannedAt.After(i.DueAt) || (!last.IsZero() && c.PlannedAt.Before(last)) {
			return &ValidationError{Field: fmt.Sprintf("checkpoints[%d].planned_at", n), Message: "检查点时间必须递增且位于本轮期限内"}
		}
		if c.AllowedDeviation < 0 {
			return &ValidationError{Field: fmt.Sprintf("checkpoints[%d].allowed_deviation", n), Message: "允许偏差不能为负数"}
		}
		c.Status = "待复测"
		last = c.PlannedAt
	}
	i.RetestCheckpoints = append([]RetestCheckpoint(nil), checkpoints...)
	i.Revision++
	i.UpdatedAt = now
	i.appendEvent("复测计划更新", actor, requestID, map[string]interface{}{"checkpoints": checkpoints})
	return nil
}

func (i *PreservationIncident) RetestGate(now time.Time) []string {
	var missing []string
	for n := range i.RetestCheckpoints {
		c := &i.RetestCheckpoints[n]
		if c.Status == "待复测" && now.After(c.PlannedAt.Add(c.AllowedDeviation)) {
			c.Status = "已错过"
		}
		if c.Required && c.Status != "已完成" {
			missing = append(missing, c.ID)
		}
	}
	return missing
}

func (i *PreservationIncident) RefreshRetestSummary(now time.Time) {
	i.NextRetestAt = nil
	i.PendingRetests = nil
	i.CompletedRetests = nil
	i.MissedRetests = nil
	for n := range i.RetestCheckpoints {
		c := &i.RetestCheckpoints[n]
		if c.Status == "待复测" && now.After(c.PlannedAt.Add(c.AllowedDeviation)) {
			c.Status = "已错过"
		}
		switch c.Status {
		case "待复测":
			i.PendingRetests = append(i.PendingRetests, c.ID)
			if i.NextRetestAt == nil || c.PlannedAt.Before(*i.NextRetestAt) {
				t := c.PlannedAt
				i.NextRetestAt = &t
			}
		case "已完成":
			i.CompletedRetests = append(i.CompletedRetests, c.ID)
		case "已错过":
			i.MissedRetests = append(i.MissedRetests, c.ID)
		}
	}
}

func ValidateAcceptanceStandards(comparisons []ReadingComparison, failures []string, standards []AcceptanceStandard, now time.Time, rules RuleSnapshot) error {
	if len(failures) != len(standards) {
		return &ValidationError{Field: "acceptance_standards", Message: "必须为每个不合格指标提供验收标准"}
	}
	want := map[string]bool{}
	for _, m := range failures {
		want[m] = true
	}
	seen := map[string]bool{}
	for n, s := range standards {
		if !want[s.Metric] || seen[s.Metric] {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].metric", n), Message: "标准指标集合必须与不合格结果完全一致"}
		}
		seen[s.Metric] = true
		if strings.TrimSpace(s.Unit) == "" || strings.TrimSpace(s.EvidenceRequirement) == "" {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d]", n), Message: "单位和证据要求不能为空"}
		}
		if s.TargetMin == nil && s.TargetMax == nil {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].target", n), Message: "必须填写目标值或范围"}
		}
		if s.TargetMin != nil && s.TargetMax != nil && *s.TargetMin > *s.TargetMax {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].target", n), Message: "目标范围无效"}
		}
		if s.MinimumStableFor <= 0 {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].minimum_stable_for", n), Message: "最低稳定时长必须为正数"}
		}
		if s.Deadline.IsZero() || !s.Deadline.After(now) {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].deadline", n), Message: "整改期限必须晚于退回时间"}
		}
		// 仅允许现有比较集中的单位，避免不可换算单位进入下一轮。
		for _, c := range comparisons {
			if c.Metric == s.Metric && c.Unit != s.Unit && !convertibleUnit(c.Unit, s.Unit) {
				return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].unit", n), Message: "目标单位不可与现有比较集换算"}
			}
		}
		minRule, maxRule := 0.0, 0.0
		switch s.Metric {
		case "湿度":
			minRule, maxRule = rules.HumidityMin, rules.HumidityMax
		case "温度":
			minRule, maxRule = rules.TemperatureMin, rules.TemperatureMax
		case "光照":
			maxRule = rules.LightMax
		case "污染物":
			maxRule = rules.PollutantMax
		}
		if (s.TargetMin != nil && minRule != 0 && *s.TargetMin < minRule) || (s.TargetMax != nil && maxRule != 0 && *s.TargetMax > maxRule) {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].target", n), Message: "目标范围不得宽于冻结规则"}
		}
	}
	return nil
}

func convertibleUnit(a, b string) bool {
	return a == b || (a == "%" && b == "%RH") || (a == "%RH" && b == "%")
}

func ValidateAcceptanceResults(comparisons []ReadingComparison, standards []AcceptanceStandard, readings []EnvironmentalReading, now time.Time) error {
	for n, standard := range standards {
		matched := false
		for _, comparison := range comparisons {
			if comparison.Metric != standard.Metric {
				continue
			}
			matched = true
			if !comparison.WithinThreshold {
				return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d]", n), Message: "目标范围尚未满足: " + standard.Metric}
			}
			if comparison.EffectValue != nil && ((standard.TargetMin != nil && *comparison.EffectValue < *standard.TargetMin) || (standard.TargetMax != nil && *comparison.EffectValue > *standard.TargetMax)) {
				return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d]", n), Message: "实测值未达到目标: " + standard.Metric}
			}
		}
		if !matched {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].metric", n), Message: "缺少标准指标效果读数"}
		}
		var first, last time.Time
		evidence := false
		for _, reading := range readings {
			if reading.Phase == PhaseEffect && reading.Metric == standard.Metric {
				if first.IsZero() || reading.MeasuredAt.Before(first) {
					first = reading.MeasuredAt
				}
				if reading.MeasuredAt.After(last) {
					last = reading.MeasuredAt
				}
				if strings.TrimSpace(reading.EvidenceRef) != "" {
					evidence = true
				}
			}
		}
		if first.IsZero() || last.Sub(first) < standard.MinimumStableFor {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].minimum_stable_for", n), Message: "稳定时长尚未达到"}
		}
		if !evidence {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].evidence_requirement", n), Message: "缺少验收证据"}
		}
		if standard.Deadline.Before(now) {
			return &ValidationError{Field: fmt.Sprintf("acceptance_standards[%d].deadline", n), Message: "整改期限已到期"}
		}
	}
	_ = readings
	return nil
}
