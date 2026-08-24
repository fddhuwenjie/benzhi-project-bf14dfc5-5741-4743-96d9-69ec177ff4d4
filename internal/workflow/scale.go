package workflow

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"time"
)

type dimensionAccumulator struct {
	count, overdue, closed, responded int
	response                          time.Duration
}

func dimensionStatistics(items []*domain.PreservationIncident, dimension string, fixed []string, now time.Time) []DimensionStatistic {
	values := map[string]*dimensionAccumulator{}
	for _, key := range fixed {
		values[key] = &dimensionAccumulator{}
	}
	for _, in := range items {
		key := in.AreaID
		if dimension == "status" {
			key = string(in.Status)
		} else if dimension == "risk" {
			key = string(in.RiskLevel)
		}
		acc := values[key]
		if acc == nil {
			acc = &dimensionAccumulator{}
			values[key] = acc
		}
		acc.count++
		deadline := in.ObservedAt.Add(in.ResponseDue)
		if in.Status == domain.StatusMitigating && !in.DueAt.IsZero() {
			deadline = in.DueAt
		}
		if in.Status != domain.StatusClosed && now.After(deadline) {
			acc.overdue++
		}
		if in.Status == domain.StatusClosed {
			acc.closed++
		}
		for _, event := range in.Timeline {
			if event.EventType == "分派" {
				duration := event.OccurredAt.Sub(in.ObservedAt)
				if duration < 0 {
					duration = 0
				}
				acc.response += duration
				acc.responded++
				break
			}
		}
	}
	keys := make([]string, 0, len(values))
	if fixed != nil {
		keys = append(keys, fixed...)
	} else {
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}
	result := make([]DimensionStatistic, 0, len(keys))
	for _, key := range keys {
		acc := values[key]
		stat := DimensionStatistic{Key: key, Count: acc.count, OverdueCount: acc.overdue}
		if acc.responded > 0 {
			stat.AverageResponseSeconds = acc.response.Seconds() / float64(acc.responded)
		}
		if acc.count > 0 {
			stat.ClosureRate = float64(acc.closed) / float64(acc.count)
		}
		result = append(result, stat)
	}
	return result
}

func ruleSnapshot(rules assessment.RuleSet) domain.RuleSnapshot {
	if rules.TempMax == 0 {
		rules = assessment.DefaultRules()
	}
	if rules.StabilityWindow <= 0 {
		rules.StabilityWindow = assessment.DefaultRules().StabilityWindow
	}
	return domain.RuleSnapshot{Version: rules.Version, TemperatureMin: rules.TempMin, TemperatureMax: rules.TempMax, HumidityMin: rules.HumidityMin, HumidityMax: rules.HumidityMax, LightMax: rules.LightMax, PollutantMax: rules.PollutantMax, StabilityWindow: rules.StabilityWindow}
}

func lockedRules(in *domain.PreservationIncident, fallback assessment.RuleSet) assessment.RuleSet {
	snapshot := in.RuleSnapshot
	if snapshot.TemperatureMax == 0 {
		if fallback.TempMax == 0 {
			fallback = assessment.DefaultRules()
		}
		return fallback
	}
	return assessment.RuleSet{TempMin: snapshot.TemperatureMin, TempMax: snapshot.TemperatureMax, HumidityMin: snapshot.HumidityMin, HumidityMax: snapshot.HumidityMax, LightMax: snapshot.LightMax, PollutantMax: snapshot.PollutantMax, StabilityWindow: snapshot.StabilityWindow, Version: in.RuleSetVersion}
}

func currentRoundStability(in *domain.PreservationIncident, rules assessment.RuleSet) []domain.StabilitySummary {
	if in.Plan == nil {
		return nil
	}
	registration := make([]domain.EnvironmentalReading, 0)
	byID := map[string]domain.EnvironmentalReading{}
	for _, reading := range in.Readings {
		if reading.ReplacedByID != "" {
			continue
		}
		byID[reading.ID] = reading
		if reading.Phase != domain.PhaseEffect {
			registration = append(registration, reading)
		}
	}
	result := make([]domain.StabilitySummary, 0)
	for _, item := range in.Plan.Items {
		readings := append([]domain.EnvironmentalReading(nil), registration...)
		for _, id := range item.EffectReadingIDs {
			if reading, ok := byID[id]; ok {
				readings = append(readings, reading)
			}
		}
		result = append(result, assessment.Stability(readings, rules)...)
	}
	return result
}

func (s *Service) CorrectRegistrationReading(id string, revision int, readingID string, replacement domain.EnvironmentalReading, reason, actor, requestID string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		ReadingID, Reason, Actor string
		Replacement              domain.EnvironmentalReading
	}{readingID, reason, actor, replacement})
	if in, handled, err := s.reuse(requestID, "registration-reading-correction", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Status != domain.StatusPending {
		return nil, domain.ErrState
	}
	if in.Revision != revision {
		return nil, domain.ErrConflict
	}
	var original *domain.EnvironmentalReading
	for n := range in.Readings {
		if in.Readings[n].ID == readingID && in.Readings[n].ReplacedByID == "" && (in.Readings[n].Phase == domain.PhaseBaseline || in.Readings[n].Phase == domain.PhaseAbnormal) {
			copy := in.Readings[n]
			original = &copy
			break
		}
	}
	if original == nil {
		return nil, &domain.ValidationError{Field: "reading_id", Message: "未找到可更正的有效登记读数"}
	}
	if replacement.ID == "" {
		replacement.ID = fmt.Sprintf("%s-correction-%d", readingID, revision)
	}
	replacement.Phase = original.Phase
	if strings.TrimSpace(replacement.Metric) == "" {
		replacement.Metric = original.Metric
	}
	active := make([]domain.EnvironmentalReading, 0, len(in.Readings))
	for _, reading := range in.Readings {
		if reading.ReplacedByID == "" && reading.ID != readingID && (reading.Phase == domain.PhaseBaseline || reading.Phase == domain.PhaseAbnormal) {
			active = append(active, reading)
		}
	}
	active = append(active, replacement)
	rules := lockedRules(in, s.Rules)
	result, err := assessment.EvaluateAt(active, in.Sensitivity, in.ObservedAt, s.now(), rules)
	if err != nil {
		return nil, remapReplacementError(err, len(active)-1)
	}
	for _, normalized := range result.Normalized {
		if normalized.ID == replacement.ID {
			replacement = normalized
			break
		}
	}
	if replacement.Metric != original.Metric {
		return nil, &domain.ValidationError{Field: "replacement_reading.metric", Message: "替换读数必须保持原登记指标"}
	}
	if err = in.CorrectRegistrationReading(revision, readingID, replacement, reason, actor, requestID, s.now(), result.Level, result.Basis, result.Response, result.Intervals, result.Pairings, result.MissingBaselines, result.RuleHits); err != nil {
		return nil, err
	}
	in.Comparisons = assessment.Compare(in.Readings, rules)
	return s.commit(in, revision, requestID, "registration-reading-correction", digest)
}

func remapReplacementError(err error, replacementIndex int) error {
	validation, ok := err.(*domain.ValidationError)
	if !ok {
		return err
	}
	prefix := fmt.Sprintf("readings[%d]", replacementIndex)
	field := strings.Replace(validation.Field, prefix, "replacement_reading", 1)
	return &domain.ValidationError{Field: field, Message: validation.Message, MissingMetrics: validation.MissingMetrics, Comparisons: validation.Comparisons}
}

func (s *Service) TransferAssignee(id string, revision int, assignee, reason string, due time.Time, actor, requestID string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Assignee, Reason, Actor string
		DueAt                   time.Time
	}{assignee, reason, actor, due})
	if in, handled, err := s.reuse(requestID, "assignee-transfer", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Status != domain.StatusMitigating {
		return nil, domain.ErrState
	}
	if in.Revision != revision {
		return nil, domain.ErrConflict
	}
	snapshot := s.workloadSnapshot(in, strings.TrimSpace(assignee), due, "")
	if len(snapshot.Conflicts) > 0 {
		return nil, &domain.WorkloadConflictError{Snapshot: snapshot, Message: "新执行人的活动任务期限发生冲突"}
	}
	if err = in.TransferAssignee(revision, assignee, reason, due, actor, requestID, snapshot, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "assignee-transfer", digest)
}

func (s *Service) RecordItemsBatch(id string, revision int, completions []domain.ItemCompletion, actor, requestID string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Items []domain.ItemCompletion
		Actor string
	}{completions, actor})
	if in, handled, err := s.reuse(requestID, "items-batch", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	for n := range completions {
		if completions[n].EffectReading != nil {
			completions[n].EffectReadings = append(completions[n].EffectReadings, *completions[n].EffectReading)
		}
		for readingIndex := range completions[n].EffectReadings {
			if completions[n].EffectReadings[readingIndex].EvidenceRef == "" {
				completions[n].EffectReadings[readingIndex].EvidenceRef = strings.TrimSpace(completions[n].Evidence)
			}
			if completions[n].EffectReadings[readingIndex].EvidenceRecordedAt.IsZero() {
				completions[n].EffectReadings[readingIndex].EvidenceRecordedAt = completions[n].EffectReadings[readingIndex].MeasuredAt
			}
		}
		completions[n].EffectReadings, err = s.normalizeEffects(in, completions[n].EffectReadings)
		if err != nil {
			if validation, ok := err.(*domain.ValidationError); ok {
				validation.Field = fmt.Sprintf("items[%d].%s", n, validation.Field)
			}
			return nil, err
		}
	}
	if err = in.RecordItemsBatch(revision, completions, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	rules := lockedRules(in, s.Rules)
	in.Comparisons = assessment.Compare(in.Readings, rules)
	in.ApplyStability(currentRoundStability(in, rules))
	return s.commit(in, revision, requestID, "items-batch", digest)
}

func (s *Service) GetArchive(id string) (*domain.ArchiveSummary, error) {
	in, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Status != domain.StatusClosed || in.Archive == nil {
		return nil, domain.ErrState
	}
	if in.Archive.ChecksumStatus != "有效" {
		return nil, &domain.IntegrityError{Message: "归档报告内容与关闭时校验和不一致"}
	}
	return in.Archive, nil
}
