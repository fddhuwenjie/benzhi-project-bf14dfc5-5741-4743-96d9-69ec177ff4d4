package assessment

import (
	"fmt"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"time"
)

// PairBaselines creates one deterministic pairing for every abnormal metric.
func PairBaselines(readings []domain.EnvironmentalReading) ([]domain.BaselinePairing, []string, []domain.FieldIssue) {
	baselines := map[string][]domain.EnvironmentalReading{}
	abnormals := map[string][]domain.EnvironmentalReading{}
	for _, reading := range readings {
		if reading.ReplacedByID != "" {
			continue
		}
		switch reading.Phase {
		case domain.PhaseBaseline:
			baselines[reading.Metric] = append(baselines[reading.Metric], reading)
		case domain.PhaseAbnormal:
			abnormals[reading.Metric] = append(abnormals[reading.Metric], reading)
		}
	}
	var issues []domain.FieldIssue
	for metric, group := range baselines {
		if len(abnormals[metric]) == 0 {
			for _, reading := range group {
				issues = append(issues, domain.FieldIssue{Field: readingField(readings, reading.ID) + ".metric", Message: "baseline 必须与同一指标的 abnormal 读数配对"})
			}
		}
	}
	metrics := make([]string, 0, len(abnormals))
	for metric := range abnormals {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	pairings := make([]domain.BaselinePairing, 0, len(metrics))
	missing := make([]string, 0)
	for _, metric := range metrics {
		sortReadings(abnormals[metric])
		abnormal := abnormals[metric][0]
		pair := domain.BaselinePairing{Metric: metric, Unit: abnormal.Unit, AbnormalReadingID: abnormal.ID, Status: "baseline_missing", ValidationBasis: "未提交同指标 baseline，保留缺失状态"}
		if len(baselines[metric]) == 0 {
			missing = append(missing, metric)
			pairings = append(pairings, pair)
			continue
		}
		sortReadings(baselines[metric])
		valid := make([]domain.EnvironmentalReading, 0, len(baselines[metric]))
		for _, baseline := range baselines[metric] {
			if !baseline.MeasuredAt.Before(abnormal.MeasuredAt) {
				issues = append(issues, domain.FieldIssue{Field: readingField(readings, baseline.ID) + ".measured_at", Message: "baseline 测量时间必须早于同指标 abnormal 读数"})
				continue
			}
			valid = append(valid, baseline)
		}
		if len(valid) == 0 {
			missing = append(missing, metric)
			pairings = append(pairings, pair)
			continue
		}
		baseline := valid[len(valid)-1]
		pair.BaselineReadingID = baseline.ID
		pair.Status = "paired"
		pair.ValidationBasis = "指标一致、单位已归一化且 baseline 时间早于 abnormal"
		pairings = append(pairings, pair)
	}
	return pairings, missing, issues
}

func readingField(readings []domain.EnvironmentalReading, id string) string {
	for n, reading := range readings {
		if reading.ID == id {
			return fmt.Sprintf("readings[%d]", n)
		}
	}
	return "readings"
}

func sortReadings(readings []domain.EnvironmentalReading) {
	sort.SliceStable(readings, func(a, b int) bool {
		if readings[a].MeasuredAt.Equal(readings[b].MeasuredAt) {
			return readings[a].ID < readings[b].ID
		}
		return readings[a].MeasuredAt.Before(readings[b].MeasuredAt)
	})
}

func RuleHits(readings []domain.EnvironmentalReading, intervals []domain.AbnormalInterval, sensitivity string, rules RuleSet) []domain.RuleHit {
	if rules.TempMax == 0 {
		rules = DefaultRules()
	}
	durations := map[string]time.Duration{}
	for _, interval := range intervals {
		if interval.Duration > durations[interval.Metric] {
			durations[interval.Metric] = interval.Duration
		}
	}
	latest := map[string]domain.EnvironmentalReading{}
	for _, reading := range readings {
		current, exists := latest[reading.Metric]
		if !exists || current.MeasuredAt.Before(reading.MeasuredAt) || current.MeasuredAt.Equal(reading.MeasuredAt) && current.ID < reading.ID {
			latest[reading.Metric] = reading
		}
	}
	metrics := make([]string, 0, len(latest))
	for metric := range latest {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	bonus := 0
	if strings.EqualFold(strings.TrimSpace(sensitivity), "high") || strings.TrimSpace(sensitivity) == "高" {
		bonus = 1
	}
	hits := make([]domain.RuleHit, 0, len(metrics))
	for _, metric := range metrics {
		reading := latest[metric]
		ruleID, boundary := ruleBoundary(metric, rules)
		hits = append(hits, domain.RuleHit{RuleID: ruleID, Metric: metric, ReadingID: reading.ID, ActualValue: reading.Value, Unit: reading.Unit, Boundary: boundary, Matched: !WithinThreshold(metric, reading.Value, rules), Duration: durations[metric], Sensitivity: sensitivity, SensitivityBonus: bonus})
	}
	return hits
}

func ruleBoundary(metric string, rules RuleSet) (string, string) {
	switch metric {
	case "温度":
		return "TEMP-OUTSIDE-INCLUSIVE-RANGE", fmt.Sprintf("< %.1f 或 > %.1f", rules.TempMin, rules.TempMax)
	case "湿度":
		return "HUMIDITY-OUTSIDE-INCLUSIVE-RANGE", fmt.Sprintf("< %.1f 或 > %.1f", rules.HumidityMin, rules.HumidityMax)
	case "光照":
		return "LIGHT-ABOVE-MAX", fmt.Sprintf("> %.1f", rules.LightMax)
	case "污染物":
		return "POLLUTANT-ABOVE-MAX", fmt.Sprintf("> %.1f", rules.PollutantMax)
	default:
		return "UNKNOWN", ""
	}
}

// Stability calculates trends from active effect readings. A single in-range
// reading remains compatible with historical incidents; multiple readings must
// span the configured window and remain continuously in range.
func Stability(readings []domain.EnvironmentalReading, rules RuleSet) []domain.StabilitySummary {
	if rules.StabilityWindow <= 0 {
		rules.StabilityWindow = DefaultRules().StabilityWindow
	}
	abnormal := map[string]domain.EnvironmentalReading{}
	effects := map[string][]domain.EnvironmentalReading{}
	for _, reading := range readings {
		if reading.ReplacedByID != "" {
			continue
		}
		if reading.Phase == domain.PhaseAbnormal && !WithinThreshold(reading.Metric, reading.Value, rules) {
			current, ok := abnormal[reading.Metric]
			if !ok || current.MeasuredAt.Before(reading.MeasuredAt) {
				abnormal[reading.Metric] = reading
			}
		}
		if reading.Phase == domain.PhaseEffect {
			effects[reading.Metric] = append(effects[reading.Metric], reading)
		}
	}
	metrics := make([]string, 0, len(effects))
	for metric := range effects {
		metrics = append(metrics, metric)
	}
	sort.Strings(metrics)
	result := make([]domain.StabilitySummary, 0, len(metrics))
	for _, metric := range metrics {
		group := effects[metric]
		sortReadings(group)
		summary := domain.StabilitySummary{Metric: metric, MinimumWindow: rules.StabilityWindow, Stable: true}
		allWithin := true
		for n, reading := range group {
			point := domain.TrendPoint{ReadingID: reading.ID, MeasuredAt: reading.MeasuredAt, Value: reading.Value, Unit: reading.Unit, WithinThreshold: WithinThreshold(metric, reading.Value, rules)}
			if n > 0 {
				change := reading.Value - group[n-1].Value
				point.ChangeFromPrev = &change
			}
			if initial, ok := abnormal[metric]; ok {
				point.Recovery = recoveryPercent(metric, initial.Value, reading.Value, rules)
			}
			if !point.WithinThreshold {
				allWithin = false
				if n > 0 && summary.Trend[n-1].WithinThreshold {
					summary.Rebounded = true
				}
			}
			summary.ParticipatingReadings = append(summary.ParticipatingReadings, reading.ID)
			summary.Trend = append(summary.Trend, point)
		}
		if len(group) > 1 {
			summary.ObservedSpan = group[len(group)-1].MeasuredAt.Sub(group[0].MeasuredAt)
			summary.Stable = allWithin && summary.ObservedSpan >= rules.StabilityWindow
		} else {
			summary.Stable = allWithin
		}
		result = append(result, summary)
	}
	return result
}
