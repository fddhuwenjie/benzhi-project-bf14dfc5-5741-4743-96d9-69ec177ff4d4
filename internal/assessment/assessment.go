package assessment

import (
	"fmt"
	"math"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type RuleSet struct {
	TempMin, TempMax, HumidityMin, HumidityMax, LightMax, PollutantMax float64
	Version                                                            string
	StabilityWindow                                                    time.Duration
	Templates                                                          map[string]ThresholdTemplate
}

type ThresholdTemplate struct {
	Version                                                            string `json:"version"`
	Sensitivity                                                        string `json:"sensitivity"`
	TempMin, TempMax, HumidityMin, HumidityMax, LightMax, PollutantMax float64
	StabilityWindow                                                    time.Duration `json:"stability_window"`
	Published                                                          bool          `json:"published"`
}

type Result struct {
	Level            domain.RiskLevel
	Basis            []string
	Response         time.Duration
	Normalized       []domain.EnvironmentalReading
	Intervals        []domain.AbnormalInterval
	Pairings         []domain.BaselinePairing
	MissingBaselines []string
	RuleVersion      string
	RuleHits         []domain.RuleHit
}

type Preview struct {
	Result
	Issues []domain.FieldIssue
}

func DefaultRules() RuleSet {
	base := RuleSet{TempMin: 10, TempMax: 30, HumidityMin: 35, HumidityMax: 65, LightMax: 300, PollutantMax: 80, Version: "museum-environment-rules-v1", StabilityWindow: 2 * time.Hour}
	base.Templates = map[string]ThresholdTemplate{
		"低@v1": {Version: "v1", Sensitivity: "低", TempMin: 10, TempMax: 30, HumidityMin: 35, HumidityMax: 65, LightMax: 300, PollutantMax: 80, StabilityWindow: 2 * time.Hour, Published: true},
		"中@v1": {Version: "v1", Sensitivity: "中", TempMin: 10, TempMax: 30, HumidityMin: 35, HumidityMax: 65, LightMax: 300, PollutantMax: 80, StabilityWindow: 2 * time.Hour, Published: true},
		"高@v1": {Version: "v1", Sensitivity: "高", TempMin: 12, TempMax: 28, HumidityMin: 40, HumidityMax: 60, LightMax: 200, PollutantMax: 60, StabilityWindow: 2 * time.Hour, Published: true},
	}
	return base
}

func ResolveTemplate(rules RuleSet, sensitivity, version string) (RuleSet, error) {
	sensitivity = strings.TrimSpace(sensitivity)
	version = strings.TrimSpace(version)
	if len(rules.Templates) == 0 {
		return rules, nil
	}
	if version == "" {
		version = "v1"
	}
	key := sensitivity + "@" + version
	t, ok := rules.Templates[key]
	if !ok || !t.Published {
		return rules, fmt.Errorf("阈值模板不存在或已停用")
	}
	if t.TempMin >= t.TempMax || t.HumidityMin >= t.HumidityMax || t.LightMax <= 0 || t.PollutantMax <= 0 {
		return rules, fmt.Errorf("阈值模板边界无效")
	}
	rules.TempMin, rules.TempMax, rules.HumidityMin, rules.HumidityMax, rules.LightMax, rules.PollutantMax = t.TempMin, t.TempMax, t.HumidityMin, t.HumidityMax, t.LightMax, t.PollutantMax
	rules.Version, rules.StabilityWindow = t.Version, t.StabilityWindow
	return rules, nil
}

func EvaluateAt(readings []domain.EnvironmentalReading, sensitivity string, observed, submitted time.Time, rules RuleSet) (Result, error) {
	preview := EvaluatePreview(readings, sensitivity, observed, submitted, rules)
	if len(preview.Issues) > 0 {
		issue := preview.Issues[0]
		return preview.Result, &domain.ValidationError{Field: issue.Field, Message: issue.Message}
	}
	return preview.Result, nil
}

func EvaluatePreview(readings []domain.EnvironmentalReading, sensitivity string, observed, submitted time.Time, rules RuleSet) Preview {
	preview := Preview{}
	if len(readings) == 0 {
		preview.Issues = append(preview.Issues, domain.FieldIssue{Field: "readings", Message: "至少需要一条异常读数"})
	}
	if rules.TempMax == 0 {
		rules = DefaultRules()
	}
	if rules.Version == "" {
		rules.Version = DefaultRules().Version
	}
	if rules.StabilityWindow <= 0 {
		rules.StabilityWindow = DefaultRules().StabilityWindow
	}
	if observed.After(submitted) {
		preview.Issues = append(preview.Issues, domain.FieldIssue{Field: "observed_at", Message: "事件观测时间不得晚于登记提交时间"})
	}
	normalized := make([]domain.EnvironmentalReading, 0, len(readings))
	evidenceRefs := map[string]bool{}
	readingIDs := map[string]bool{}
	for n, reading := range readings {
		field := fmt.Sprintf("readings[%d]", n)
		if reading.ID == "" {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".id", Message: "读数标识不能为空"})
		} else if readingIDs[reading.ID] {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".id", Message: "读数标识重复"})
		}
		readingIDs[reading.ID] = true
		if reading.MeasuredAt.IsZero() || reading.MeasuredAt.After(submitted) {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".measured_at", Message: "测量时间不能为空且不得晚于登记提交时间"})
		} else if reading.MeasuredAt.After(observed) {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".measured_at", Message: "测量时间不得晚于事件观测时间"})
		}
		if strings.TrimSpace(reading.SourceNote) == "" || utf8.RuneCountInString(reading.SourceNote) > 500 {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".source_note", Message: "来源说明不能为空且不得超过 500 个字符"})
		}
		ref := strings.TrimSpace(reading.EvidenceRef)
		if ref == "" || utf8.RuneCountInString(ref) > 500 {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".evidence_ref", Message: "现场证据引用不能为空且不得超过 500 个字符"})
		} else if evidenceRefs[ref] {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".evidence_ref", Message: "现场证据引用不得复用于其他读数"})
		}
		evidenceRefs[ref] = true
		if reading.EvidenceRecordedAt.IsZero() || reading.EvidenceRecordedAt.Before(reading.MeasuredAt) || reading.EvidenceRecordedAt.After(submitted) {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".evidence_recorded_at", Message: "证据时间必须介于对应测量与登记提交之间"})
		}
		if reading.Phase == "" {
			reading.Phase = domain.PhaseAbnormal
		}
		reading.SourceNote = strings.TrimSpace(reading.SourceNote)
		reading.EvidenceRef = ref
		if reading.Phase != domain.PhaseAbnormal && reading.Phase != domain.PhaseBaseline {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".phase", Message: "登记读数阶段只能为 baseline 或 abnormal"})
			continue
		}
		norm, err := Normalize(reading)
		if err != nil {
			preview.Issues = append(preview.Issues, domain.FieldIssue{Field: field + ".unit", Message: err.Error()})
			continue
		}
		normalized = append(normalized, norm)
	}

	abnormal := filterPhase(normalized, domain.PhaseAbnormal)
	if len(abnormal) == 0 {
		preview.Issues = append(preview.Issues, domain.FieldIssue{Field: "readings", Message: "至少需要一条可研判的 abnormal 读数"})
	}
	intervals := ContinuousIntervals(abnormal, rules)
	pairings, missing, pairingIssues := PairBaselines(normalized)
	preview.Issues = append(preview.Issues, pairingIssues...)
	score, basis := scoreAbnormal(abnormal, intervals, sensitivity, rules)
	level, due := riskResult(score)
	hits := RuleHits(abnormal, intervals, sensitivity, rules)
	if len(basis) == 0 {
		basis = []string{"登记读数均在阈值内，需结合现场情况复核"}
	}
	preview.Result = Result{Level: level, Basis: basis, Response: due, Normalized: normalized, Intervals: intervals, Pairings: pairings, MissingBaselines: missing, RuleVersion: rules.Version, RuleHits: hits}
	return preview
}

// Evaluate 保留原有调用形式；HTTP 工作流使用 EvaluateAt 固定登记提交时间。
func Evaluate(readings []domain.EnvironmentalReading, sensitivity string, observed time.Time, rules RuleSet) (Result, error) {
	return EvaluateAt(readings, sensitivity, observed, time.Now().UTC(), rules)
}

func Normalize(r domain.EnvironmentalReading) (domain.EnvironmentalReading, error) {
	r.OriginalValue, r.OriginalUnit = r.Value, r.Unit
	metric := strings.ToLower(strings.TrimSpace(r.Metric))
	unit := strings.ToLower(strings.TrimSpace(r.Unit))
	switch metric {
	case "温度", "temperature":
		r.Metric = "温度"
		switch unit {
		case "℃", "°c", "c", "摄氏度":
			r.Unit = "℃"
		case "℉", "°f", "f", "华氏度":
			r.Value = (r.Value - 32) * 5 / 9
			r.Unit = "℃"
		default:
			return r, fmt.Errorf("温度单位 %q 不支持换算", r.Unit)
		}
	case "湿度", "humidity":
		r.Metric, r.Unit = "湿度", "%RH"
		if unit != "%rh" && unit != "%" {
			return r, fmt.Errorf("湿度单位 %q 不支持换算", r.OriginalUnit)
		}
	case "光照", "illuminance", "light":
		r.Metric, r.Unit = "光照", "lux"
		if unit != "lux" && unit != "lx" {
			return r, fmt.Errorf("光照单位 %q 不支持换算", r.OriginalUnit)
		}
	case "污染物", "pollutant":
		r.Metric, r.Unit = "污染物", "µg/m³"
		switch unit {
		case "µg/m³", "μg/m³", "ug/m3", "µg/m3", "μg/m3":
		default:
			return r, fmt.Errorf("污染物单位 %q 不支持换算", r.OriginalUnit)
		}
	default:
		return r, fmt.Errorf("指标 %q 不受支持", r.Metric)
	}
	if math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
		return r, fmt.Errorf("读数值必须为有限数值")
	}
	return r, nil
}

func WithinThreshold(metric string, value float64, rules RuleSet) bool {
	if rules.TempMax == 0 {
		rules = DefaultRules()
	}
	switch metric {
	case "温度":
		return value >= rules.TempMin && value <= rules.TempMax
	case "湿度":
		return value >= rules.HumidityMin && value <= rules.HumidityMax
	case "光照":
		return value <= rules.LightMax
	case "污染物":
		return value <= rules.PollutantMax
	}
	return false
}

func ContinuousIntervals(readings []domain.EnvironmentalReading, rules RuleSet) []domain.AbnormalInterval {
	groups := map[string][]domain.EnvironmentalReading{}
	for _, r := range readings {
		groups[r.Metric] = append(groups[r.Metric], r)
	}
	var intervals []domain.AbnormalInterval
	for metric, group := range groups {
		sort.SliceStable(group, func(a, b int) bool {
			if group[a].MeasuredAt.Equal(group[b].MeasuredAt) {
				return group[a].ID < group[b].ID
			}
			return group[a].MeasuredAt.Before(group[b].MeasuredAt)
		})
		var current *domain.AbnormalInterval
		for _, r := range group {
			if WithinThreshold(metric, r.Value, rules) {
				if current != nil {
					intervals = append(intervals, *current)
					current = nil
				}
				continue
			}
			if current == nil {
				current = &domain.AbnormalInterval{Metric: metric, StartedAt: r.MeasuredAt, EndedAt: r.MeasuredAt, ReadingIDs: []string{r.ID}}
			} else {
				current.EndedAt = r.MeasuredAt
				current.ReadingIDs = append(current.ReadingIDs, r.ID)
			}
			current.Duration = current.EndedAt.Sub(current.StartedAt)
		}
		if current != nil {
			intervals = append(intervals, *current)
		}
	}
	sort.SliceStable(intervals, func(a, b int) bool {
		if intervals[a].Metric == intervals[b].Metric {
			return intervals[a].StartedAt.Before(intervals[b].StartedAt)
		}
		return intervals[a].Metric < intervals[b].Metric
	})
	return intervals
}

func Compare(readings []domain.EnvironmentalReading, rules RuleSet) []domain.ReadingComparison {
	metrics := map[string]bool{}
	for _, r := range readings {
		if r.Phase == domain.PhaseAbnormal {
			metrics[r.Metric] = true
		}
	}
	names := make([]string, 0, len(metrics))
	for metric := range metrics {
		names = append(names, metric)
	}
	sort.Strings(names)
	result := make([]domain.ReadingComparison, 0, len(names))
	for _, metric := range names {
		var baseline, abnormal, effect *domain.EnvironmentalReading
		for n := range readings {
			r := &readings[n]
			if r.Metric != metric || r.ReplacedByID != "" {
				continue
			}
			switch r.Phase {
			case domain.PhaseBaseline:
				if baseline == nil || !r.MeasuredAt.Before(baseline.MeasuredAt) {
					baseline = r
				}
			case domain.PhaseAbnormal:
				if !WithinThreshold(metric, r.Value, rules) && (abnormal == nil || !r.MeasuredAt.Before(abnormal.MeasuredAt)) {
					abnormal = r
				}
			case domain.PhaseEffect:
				if effect == nil || !r.MeasuredAt.Before(effect.MeasuredAt) {
					effect = r
				}
			}
		}
		if abnormal == nil {
			continue
		}
		c := domain.ReadingComparison{Metric: metric, Unit: abnormal.Unit, AbnormalReadingID: abnormal.ID, AbnormalValue: abnormal.Value}
		if baseline != nil {
			v := baseline.Value
			c.BaselineReadingID, c.BaselineValue = baseline.ID, &v
		}
		if effect != nil {
			v, change := effect.Value, effect.Value-abnormal.Value
			recovered := recoveryPercent(metric, abnormal.Value, effect.Value, rules)
			c.EffectReadingID, c.EffectValue, c.Change, c.RecoveryPercent = effect.ID, &v, &change, &recovered
			c.WithinThreshold = WithinThreshold(metric, effect.Value, rules)
		}
		result = append(result, c)
	}
	return result
}

func filterPhase(readings []domain.EnvironmentalReading, phase domain.ReadingPhase) []domain.EnvironmentalReading {
	var out []domain.EnvironmentalReading
	for _, r := range readings {
		if r.Phase == phase {
			out = append(out, r)
		}
	}
	return out
}

func scoreAbnormal(readings []domain.EnvironmentalReading, intervals []domain.AbnormalInterval, sensitivity string, rules RuleSet) (int, []string) {
	metricScore := map[string]int{}
	metricBasis := map[string]string{}
	for _, r := range readings {
		if WithinThreshold(r.Metric, r.Value, rules) {
			continue
		}
		score := 1
		switch r.Metric {
		case "温度":
			score, metricBasis[r.Metric] = 2, fmt.Sprintf("温度 %.1f℃ 超出 %.0f~%.0f℃", r.Value, rules.TempMin, rules.TempMax)
			if r.Value > rules.TempMax+10 || r.Value < rules.TempMin-10 {
				score = 4
				metricBasis[r.Metric] += "，越界幅度严重"
			}
		case "湿度":
			score, metricBasis[r.Metric] = 2, fmt.Sprintf("湿度 %.1f%%RH 超出 %.0f~%.0f%%RH", r.Value, rules.HumidityMin, rules.HumidityMax)
			if r.Value > rules.HumidityMax+20 || r.Value < rules.HumidityMin-20 {
				score = 4
				metricBasis[r.Metric] += "，越界幅度严重"
			}
		case "光照":
			metricBasis[r.Metric] = fmt.Sprintf("光照 %.1f lux 超过 %.0f lux", r.Value, rules.LightMax)
			if r.Value > rules.LightMax*2 {
				score = 3
				metricBasis[r.Metric] += "，越界幅度严重"
			}
		case "污染物":
			score, metricBasis[r.Metric] = 3, fmt.Sprintf("污染物 %.1f µg/m³ 超过 %.0f µg/m³", r.Value, rules.PollutantMax)
			if r.Value > rules.PollutantMax*2 {
				score = 4
				metricBasis[r.Metric] += "，越界幅度严重"
			}
		}
		if score > metricScore[r.Metric] {
			metricScore[r.Metric] = score
		}
	}
	total := 0
	var basis []string
	for metric, score := range metricScore {
		total += score
		basis = append(basis, metricBasis[metric])
	}
	sort.Strings(basis)
	maxDuration := time.Duration(0)
	for _, interval := range intervals {
		if interval.Duration > maxDuration {
			maxDuration = interval.Duration
		}
	}
	if maxDuration >= 6*time.Hour {
		total += 2
		basis = append(basis, fmt.Sprintf("连续超标时长 %s，已达到六小时", maxDuration))
	} else if maxDuration >= time.Hour {
		total++
		basis = append(basis, fmt.Sprintf("连续超标时长 %s", maxDuration))
	}
	if strings.Contains(strings.ToLower(sensitivity), "高") || strings.Contains(strings.ToLower(sensitivity), "high") {
		total++
		basis = append(basis, "藏品敏感级别为高")
	}
	return total, basis
}

func riskResult(score int) (domain.RiskLevel, time.Duration) {
	switch {
	case score >= 5:
		return domain.RiskCritical, 4 * time.Hour
	case score >= 3:
		return domain.RiskHigh, 24 * time.Hour
	case score >= 1:
		return domain.RiskMedium, 72 * time.Hour
	default:
		return domain.RiskLow, 7 * 24 * time.Hour
	}
}

func recoveryPercent(metric string, abnormal, effect float64, rules RuleSet) float64 {
	distance := func(value float64) float64 {
		switch metric {
		case "温度":
			if value < rules.TempMin {
				return rules.TempMin - value
			}
			if value > rules.TempMax {
				return value - rules.TempMax
			}
		case "湿度":
			if value < rules.HumidityMin {
				return rules.HumidityMin - value
			}
			if value > rules.HumidityMax {
				return value - rules.HumidityMax
			}
		case "光照":
			return math.Max(0, value-rules.LightMax)
		case "污染物":
			return math.Max(0, value-rules.PollutantMax)
		}
		return 0
	}
	start := distance(abnormal)
	if start == 0 {
		return 100
	}
	value := (start - distance(effect)) / start * 100
	if value > 100 {
		return 100
	}
	return value
}
