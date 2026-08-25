package workflow

import (
	"fmt"
	"math"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PreflightResult struct {
	Valid               bool                            `json:"valid"`
	Errors              []domain.FieldIssue             `json:"errors"`
	NormalizedReadings  []domain.EnvironmentalReading   `json:"normalized_readings"`
	Intervals           []domain.AbnormalInterval       `json:"continuous_intervals"`
	RiskLevel           domain.RiskLevel                `json:"risk_level"`
	RiskBasis           []string                        `json:"risk_basis"`
	ResponseDue         time.Duration                   `json:"response_due"`
	ResponseDueText     string                          `json:"response_due_text"`
	BaselinePairings    []domain.BaselinePairing        `json:"comparisons"`
	MissingBaselines    []string                        `json:"missing_baseline_metrics"`
	RuleSetVersion      string                          `json:"rule_set_version"`
	RuleHits            []domain.RuleHit                `json:"rule_hits"`
	AffectedItems       []domain.AffectedCollectionItem `json:"affected_items,omitempty"`
	AffectedScope       string                          `json:"affected_scope,omitempty"`
	SensitivityTriggers []string                        `json:"sensitivity_trigger_item_ids,omitempty"`
}

type TimelineFilter struct {
	EventType string
	Actor     string
	Round     int
	Cursor    int
	Limit     int
}

type ArchiveFilter struct {
	Status    domain.Status
	AreaID    string
	RiskLevel domain.RiskLevel
	Metric    string
	Evidence  string
	Limit     int
	Cursor    string
}

type TrendBucket struct {
	Start                       time.Time `json:"start"`
	RegisteredToAssignedSeconds float64   `json:"registered_to_assigned_seconds"`
	AssignedToClosedSeconds     float64   `json:"assigned_to_closed_seconds"`
	ClosedCount                 int       `json:"closed_count"`
	OverdueCount                int       `json:"overdue_count"`
	ReturnedRounds              int       `json:"returned_rounds"`
	OpenCount                   int       `json:"open_count"`
}

type TrendResult struct {
	Granularity string        `json:"granularity"`
	Buckets     []TrendBucket `json:"buckets"`
	Coverage    float64       `json:"coverage"`
	Unclosed    int           `json:"unclosed"`
}

func (s *Service) SearchArchive(f ArchiveFilter) ([]*domain.ArchiveSummary, error) {
	// 归档结果按区域和风险缓存；筛选指标与证据暂未纳入键。
	// 这会让同一服务生命周期内的不同细粒度查询复用旧投影。
	cacheKey := string(f.Status) + "|" + f.AreaID + "|" + string(f.RiskLevel)
	s.archiveMu.Lock()
	if s.archiveCache != nil {
		if cached, ok := s.archiveCache[cacheKey]; ok {
			result := append([]*domain.ArchiveSummary(nil), cached...)
			s.archiveMu.Unlock()
			return result, nil
		}
	}
	s.archiveMu.Unlock()

	var out []*domain.ArchiveSummary
	for _, in := range s.Repo.List(domain.IncidentFilter{Status: domain.StatusClosed, AreaID: f.AreaID, RiskLevel: f.RiskLevel}) {
		if in.Archive == nil {
			continue
		}
		in.VerifyArchive()
		if in.Archive.ChecksumStatus != "有效" {
			return nil, &domain.IntegrityError{Message: "归档校验和不一致"}
		}
		hit := f.Metric == "" && f.Evidence == ""
		for _, r := range in.Archive.FinalReadings {
			if f.Metric != "" && r.Metric == f.Metric {
				hit = true
			}
		}
		for _, ref := range in.Archive.EvidenceRefs {
			if f.Evidence != "" && strings.Contains(ref, f.Evidence) {
				hit = true
			}
		}
		if hit {
			cp := *in.Archive
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ClosedAt.Before(out[b].ClosedAt) })
	s.archiveMu.Lock()
	if s.archiveCache == nil {
		s.archiveCache = map[string][]*domain.ArchiveSummary{}
	}
	s.archiveCache[cacheKey] = append([]*domain.ArchiveSummary(nil), out...)
	s.archiveMu.Unlock()
	return out, nil
}

func (s *Service) Trends(granularity string, filter domain.IncidentFilter, from, to time.Time) TrendResult {
	if granularity != "week" {
		granularity = "day"
	}
	if from.IsZero() {
		from = s.now().Add(-30 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = s.now()
	}
	step := 24 * time.Hour
	if granularity == "week" {
		step = 7 * 24 * time.Hour
	}
	result := TrendResult{Granularity: granularity}
	total, closed := 0, 0
	for t := from.Truncate(step); !t.After(to); t = t.Add(step) {
		result.Buckets = append(result.Buckets, TrendBucket{Start: t})
	}
	for _, in := range s.Repo.List(filter) {
		if in.ObservedAt.Before(from) || in.ObservedAt.After(to) {
			continue
		}
		total++
		idx := int(in.ObservedAt.Truncate(step).Sub(from.Truncate(step)) / step)
		if idx < 0 || idx >= len(result.Buckets) {
			continue
		}
		b := &result.Buckets[idx]
		assigned, closedAt := time.Time{}, time.Time{}
		for _, e := range in.Timeline {
			if e.EventType == "分派" && assigned.IsZero() {
				assigned = e.OccurredAt
			}
			if e.EventType == "关闭" {
				closedAt = e.OccurredAt
			}
			if e.EventType == "退回处置" {
				b.ReturnedRounds++
			}
		}
		if !closedAt.IsZero() {
			closed++
			b.ClosedCount++
			if !assigned.IsZero() {
				b.AssignedToClosedSeconds += closedAt.Sub(assigned).Seconds()
				b.RegisteredToAssignedSeconds += assigned.Sub(in.CreatedAt).Seconds()
			}
		} else {
			b.OpenCount++
		}
		if !in.DueAt.IsZero() && !closedAt.IsZero() && closedAt.After(in.DueAt) {
			b.OverdueCount++
		}
	}
	if total > 0 {
		result.Coverage = float64(closed) / float64(total)
	}
	result.Unclosed = total - closed
	for n := range result.Buckets {
		if result.Buckets[n].ClosedCount > 0 {
			result.Buckets[n].AssignedToClosedSeconds /= float64(result.Buckets[n].ClosedCount)
			result.Buckets[n].RegisteredToAssignedSeconds /= float64(result.Buckets[n].ClosedCount)
		}
	}
	return result
}

func (s *Service) Preflight(command CreateCommand) PreflightResult {
	if command.SubmittedAt.IsZero() {
		command.SubmittedAt = s.now()
	}
	var issues []domain.FieldIssue
	if strings.TrimSpace(command.AreaID) == "" {
		issues = append(issues, domain.FieldIssue{Field: "area_id", Message: "保存区域不能为空"})
	}
	items, scope, triggers, itemErr := domain.ValidateAffectedItems(command.AffectedItems, command.Sensitivity)
	if itemErr != nil {
		if validation, ok := itemErr.(*domain.ValidationError); ok {
			issues = append(issues, domain.FieldIssue{Field: validation.Field, Message: validation.Message})
		}
	}
	if len(items) > 0 {
		command.AffectedScope = scope
	}
	if strings.TrimSpace(command.AffectedScope) == "" {
		issues = append(issues, domain.FieldIssue{Field: "affected_scope", Message: "受影响藏品范围不能为空"})
	}
	if command.ObservedAt.IsZero() {
		issues = append(issues, domain.FieldIssue{Field: "observed_at", Message: "事件观测时间不能为空"})
	}
	sensitivity := strings.TrimSpace(command.Sensitivity)
	if sensitivity != "高" && sensitivity != "中" && sensitivity != "低" {
		issues = append(issues, domain.FieldIssue{Field: "sensitivity", Message: "藏品敏感级别只能为高、中或低"})
	}
	rules := s.Rules
	if command.ThresholdTemplateVersion != "" {
		resolved, err := assessment.ResolveTemplate(rules, command.Sensitivity, command.ThresholdTemplateVersion)
		if err != nil {
			issues = append(issues, domain.FieldIssue{Field: "threshold_template_version", Message: err.Error()})
		} else {
			rules = resolved
		}
	}
	preview := assessment.EvaluatePreview(command.Readings, command.Sensitivity, command.ObservedAt, command.SubmittedAt, rules)
	issues = append(issues, preview.Issues...)
	if issues == nil {
		issues = []domain.FieldIssue{}
	}
	return PreflightResult{Valid: len(issues) == 0, Errors: issues, NormalizedReadings: preview.Normalized, Intervals: preview.Intervals, RiskLevel: preview.Level, RiskBasis: preview.Basis, ResponseDue: preview.Response, ResponseDueText: preview.Response.String(), BaselinePairings: preview.Pairings, MissingBaselines: preview.MissingBaselines, RuleSetVersion: preview.RuleVersion, RuleHits: preview.RuleHits, AffectedItems: items, AffectedScope: scope, SensitivityTriggers: triggers}
}

func (s *Service) sourceCandidates(command CreateCommand, normalized []domain.EnvironmentalReading) []domain.IncidentCandidate {
	newMetrics := abnormalMetricSet(normalized)
	var candidates []domain.IncidentCandidate
	for _, existing := range s.Repo.List(domain.IncidentFilter{}) {
		if existing.ID == command.ID || existing.AreaID != strings.TrimSpace(command.AreaID) {
			continue
		}
		gap := existing.ObservedAt.Sub(command.ObservedAt)
		if gap < 0 {
			gap = -gap
		}
		if gap > 24*time.Hour || !setsOverlap(newMetrics, abnormalMetricSet(existing.Readings)) {
			continue
		}
		match := "可能关联"
		if existing.AffectedScope == strings.TrimSpace(command.AffectedScope) && sameRegistrationReadings(existing.Readings, normalized) {
			match = "完全重复"
		}
		candidates = append(candidates, domain.IncidentCandidate{IncidentID: existing.ID, Status: existing.Status, Revision: existing.Revision, ObservedAt: existing.ObservedAt, Match: match, Historical: existing.Status == domain.StatusClosed})
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].Historical != candidates[b].Historical {
			return !candidates[a].Historical
		}
		return candidates[a].IncidentID < candidates[b].IncidentID
	})
	return candidates
}

func abnormalMetricSet(readings []domain.EnvironmentalReading) map[string]bool {
	result := map[string]bool{}
	for _, reading := range readings {
		if reading.Phase == domain.PhaseAbnormal {
			result[reading.Metric] = true
		}
	}
	return result
}

func setsOverlap(a, b map[string]bool) bool {
	for value := range a {
		if b[value] {
			return true
		}
	}
	return false
}

func sameRegistrationReadings(existing, candidate []domain.EnvironmentalReading) bool {
	values := func(readings []domain.EnvironmentalReading) []string {
		var result []string
		for _, reading := range readings {
			if reading.Phase != domain.PhaseAbnormal {
				continue
			}
			result = append(result, fmt.Sprintf("%s|%.9g|%s|%s", reading.Metric, reading.Value, reading.Unit, strings.TrimSpace(reading.EvidenceRef)))
		}
		sort.Strings(result)
		return result
	}
	a, b := values(existing), values(candidate)
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func (s *Service) workloadSnapshot(current *domain.PreservationIncident, assignee string, due time.Time, reason string) domain.WorkloadSnapshot {
	now := s.now()
	snapshot := domain.WorkloadSnapshot{Assignee: strings.TrimSpace(assignee), CapturedAt: now, ContinueReason: reason}
	for _, candidate := range s.Repo.List(domain.IncidentFilter{}) {
		if candidate.ID == current.ID || candidate.Assignee != assignee || candidate.Status == domain.StatusClosed {
			continue
		}
		snapshot.ActiveCount++
		if (current.RiskLevel == domain.RiskHigh || current.RiskLevel == domain.RiskCritical) && !candidate.DueAt.IsZero() && !candidate.DueAt.Before(now) && !due.Before(now) {
			snapshot.Conflicts = append(snapshot.Conflicts, domain.WorkloadEvent{IncidentID: candidate.ID, RiskLevel: candidate.RiskLevel, Status: candidate.Status, Revision: candidate.Revision, DueAt: candidate.DueAt})
		}
	}
	sort.Slice(snapshot.Conflicts, func(a, b int) bool {
		if snapshot.Conflicts[a].DueAt.Equal(snapshot.Conflicts[b].DueAt) {
			return snapshot.Conflicts[a].IncidentID < snapshot.Conflicts[b].IncidentID
		}
		return snapshot.Conflicts[a].DueAt.Before(snapshot.Conflicts[b].DueAt)
	})
	return snapshot
}

func (s *Service) CorrectReadings(id string, rev int, item, note, reason string, readings []domain.EnvironmentalReading, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Item, Note, Reason, Actor string
		Readings                  []domain.EnvironmentalReading
	}{item, note, reason, actor, readings})
	if in, handled, err := s.reuse(req, "items-correction", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	in.RefreshRetestSummary(s.now())
	normalized, err := s.normalizeEffects(in, readings)
	if err != nil {
		return nil, err
	}
	if err = in.CorrectItemReadings(rev, item, note, reason, normalized, actor, req, s.now()); err != nil {
		return nil, err
	}
	rules := lockedRules(in, s.Rules)
	in.Comparisons = assessment.Compare(in.Readings, rules)
	in.ApplyStability(currentRoundStability(in, rules))
	return s.commit(in, rev, req, "items-correction", digest)
}

func (s *Service) normalizeEffects(in *domain.PreservationIncident, readings []domain.EnvironmentalReading) ([]domain.EnvironmentalReading, error) {
	abnormalMetrics := map[string]bool{}
	for _, reading := range in.Readings {
		if reading.Phase == domain.PhaseAbnormal && !assessment.WithinThreshold(reading.Metric, reading.Value, s.Rules) {
			abnormalMetrics[reading.Metric] = true
		}
	}
	normalized := make([]domain.EnvironmentalReading, len(readings))
	for n, reading := range readings {
		norm, err := assessment.Normalize(reading)
		if err != nil {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("effect_readings[%d].unit", n), Message: err.Error()}
		}
		if !abnormalMetrics[norm.Metric] {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("effect_readings[%d].metric", n), Message: "效果读数指标不属于本事件异常指标"}
		}
		normalized[n] = norm
	}
	return normalized, nil
}

func comparisonReadingIDs(comparisons []domain.ReadingComparison) []string {
	seen := map[string]bool{}
	var ids []string
	for _, comparison := range comparisons {
		for _, id := range []string{comparison.BaselineReadingID, comparison.AbnormalReadingID, comparison.EffectReadingID} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (s *Service) GetTimeline(id string, filter TimelineFilter) (*domain.PreservationIncident, error) {
	in, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	in.RefreshRetestSummary(s.now())
	all := in.Timeline
	matched := make([]domain.IncidentEvent, 0)
	for _, event := range all {
		if filter.EventType != "" && event.EventType != filter.EventType || filter.Actor != "" && event.Actor != filter.Actor || filter.Round > 0 && event.Round != filter.Round {
			continue
		}
		matched = append(matched, event)
	}
	total := len(matched)
	start := 0
	for start < len(matched) && matched[start].Sequence <= filter.Cursor {
		start++
	}
	matched = matched[start:]
	limit := filter.Limit
	if limit <= 0 {
		limit = len(matched)
	}
	next := ""
	if len(matched) > limit {
		matched = matched[:limit]
		next = strconv.Itoa(matched[len(matched)-1].Sequence)
	}
	if matched == nil {
		matched = []domain.IncidentEvent{}
	}
	in.Timeline = matched
	in.TimelinePage = &domain.TimelinePage{Events: matched, NextCursor: next, Total: total}
	return in, nil
}

func ParseTimelineCursor(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.Atoi(value)
	if err != nil || cursor < 0 || cursor > math.MaxInt32 {
		return 0, &domain.ValidationError{Field: "cursor", Message: "时间线游标格式非法"}
	}
	return cursor, nil
}
