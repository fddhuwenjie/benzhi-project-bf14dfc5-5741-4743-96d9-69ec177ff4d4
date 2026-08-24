package workflow

import (
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"time"
)

func (s *Service) PreviewReassessment(id string, revision int, templateVersion string) (domain.ReassessmentPreview, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return domain.ReassessmentPreview{}, err
	}
	if in.Status != domain.StatusPending {
		return domain.ReassessmentPreview{}, domain.ErrState
	}
	if in.Revision != revision {
		return domain.ReassessmentPreview{}, domain.ErrConflict
	}
	rules, err := assessment.ResolveTemplate(s.Rules, in.Sensitivity, templateVersion)
	if err != nil {
		return domain.ReassessmentPreview{}, &domain.ValidationError{Field: "template_version", Message: err.Error()}
	}
	active := []domain.EnvironmentalReading{}
	for _, r := range in.Readings {
		if r.ReplacedByID == "" && (r.Phase == domain.PhaseBaseline || r.Phase == domain.PhaseAbnormal) {
			active = append(active, r)
		}
	}
	result, err := assessment.EvaluateAt(active, in.Sensitivity, in.ObservedAt, s.now(), rules)
	if err != nil {
		return domain.ReassessmentPreview{}, err
	}
	oldHits := map[string]domain.RuleHit{}
	for _, h := range in.RuleHits {
		oldHits[h.Metric] = h
	}
	newHits := map[string]domain.RuleHit{}
	for _, h := range result.RuleHits {
		newHits[h.Metric] = h
	}
	metrics := map[string]bool{}
	for m := range oldHits {
		metrics[m] = true
	}
	for m := range newHits {
		metrics[m] = true
	}
	names := make([]string, 0, len(metrics))
	for m := range metrics {
		names = append(names, m)
	}
	sort.Strings(names)
	diffs := make([]domain.RuleDiff, 0, len(names))
	var added, removed []domain.RuleHit
	for _, m := range names {
		o, ook := oldHits[m]
		n, nok := newHits[m]
		d := domain.RuleDiff{Metric: m}
		if ook {
			d.OldBoundary, d.OldMatched = o.Boundary, o.Matched
		}
		if nok {
			d.NewBoundary, d.NewMatched = n.Boundary, n.Matched
		}
		if !ook || !nok || o.Matched != n.Matched || o.Boundary != n.Boundary {
			if nok && n.Matched && !ook {
				added = append(added, n)
			}
			if ook && o.Matched && (!nok || !n.Matched) {
				removed = append(removed, o)
			}
			if n.Matched && !o.Matched {
				d.Reason = "候选规则新增命中"
			}
			if !n.Matched && o.Matched {
				d.Reason = "候选规则消除命中"
			}
			diffs = append(diffs, d)
		}
	}
	p := domain.ReassessmentPreview{IncidentID: id, Revision: revision, TemplateVersion: result.RuleVersion, OldRuleSnapshot: in.RuleSnapshot, CandidateRuleSnapshot: ruleSnapshot(rules), OldRiskLevel: in.RiskLevel, CandidateRiskLevel: result.Level, OldRiskBasis: append([]string(nil), in.RiskBasis...), CandidateRiskBasis: append([]string(nil), result.Basis...), CandidateHits: result.RuleHits, RuleDiffs: diffs, AddedHits: added, RemovedHits: removed, MissingBaselines: result.MissingBaselines, ResponseDue: result.Response, CreatedAt: s.now()}
	p.Checksum = domain.ReassessmentChecksum(id, revision, templateVersion, p)
	return p, nil
}

func (s *Service) ConfirmReassessment(id string, revision int, templateVersion, checksum, actor, requestID, explanation string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct{ T, C, A, E string }{templateVersion, checksum, actor, explanation})
	if in, ok, err := s.reuse(requestID, "assessment-reassessment", id, digest); ok || err != nil {
		return in, err
	}
	p, err := s.PreviewReassessment(id, revision, templateVersion)
	if err != nil {
		return nil, err
	}
	if checksum == "" || checksum != p.Checksum {
		return nil, &domain.ValidationError{Field: "preview_checksum", Message: "预览校验值已过期，请重新预览"}
	}
	if p.CandidateRiskLevel < p.OldRiskLevel && strings.TrimSpace(explanation) == "" {
		return nil, &domain.ValidationError{Field: "explanation", Message: "风险降级必须提交人工复核说明"}
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ReplaceAssessment(revision, p, actor, requestID, explanation, s.now()); err != nil {
		return nil, err
	}
	in.Comparisons = assessment.Compare(in.Readings, pRulesFromSnapshot(p.CandidateRuleSnapshot))
	return s.commit(in, revision, requestID, "assessment-reassessment", digest)
}

func pRulesFromSnapshot(snap domain.RuleSnapshot) assessment.RuleSet {
	return assessment.RuleSet{Version: snap.Version, TempMin: snap.TemperatureMin, TempMax: snap.TemperatureMax, HumidityMin: snap.HumidityMin, HumidityMax: snap.HumidityMax, LightMax: snap.LightMax, PollutantMax: snap.PollutantMax, StabilityWindow: snap.StabilityWindow}
}

func (s *Service) InvalidateReadings(id string, revision int, ids []string, reason, evidence, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		IDs                     []string
		Reason, Evidence, Actor string
	}{ids, reason, evidence, actor})
	if in, ok, err := s.reuse(requestID, "reading-invalidation", id, digest); ok || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Status != domain.StatusPending {
		return nil, domain.ErrState
	}
	active := []domain.EnvironmentalReading{}
	for _, r := range in.Readings {
		if r.ReplacedByID == "" && (r.Phase == domain.PhaseBaseline || r.Phase == domain.PhaseAbnormal) {
			skip := false
			for _, x := range ids {
				if x == r.ID {
					skip = true
				}
			}
			if !skip {
				active = append(active, r)
			}
		}
	}
	result, err := assessment.EvaluateAt(active, in.Sensitivity, in.ObservedAt, s.now(), lockedRules(in, s.Rules))
	if err != nil {
		return nil, err
	}
	if err = in.InvalidateReadings(revision, ids, reason, evidence, actor, requestID, s.now(), result.Level, result.Basis, result.Response, result.Intervals, result.Pairings, result.MissingBaselines, result.RuleHits); err != nil {
		return nil, err
	}
	in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	return s.commit(in, revision, requestID, "reading-invalidation", digest)
}

type AssigneeRecommendation struct {
	Assignee      string                 `json:"assignee"`
	Score         float64                `json:"score"`
	Reasons       []string               `json:"reasons"`
	Conflicts     []domain.WorkloadEvent `json:"conflicts"`
	Recommendable bool                   `json:"recommendable"`
}
type RecommendationPreview struct {
	IncidentID      string                   `json:"incident_id"`
	Revision        int                      `json:"revision"`
	Checksum        string                   `json:"recommendation_checksum"`
	Recommendations []AssigneeRecommendation `json:"recommendations"`
	CreatedAt       time.Time                `json:"created_at"`
}

type ClosureMetric struct {
	Area                   string   `json:"area"`
	Metric                 string   `json:"metric"`
	EventCount             int      `json:"event_count"`
	RecurrenceCount        int      `json:"recurrence_count"`
	AverageResponseSeconds float64  `json:"average_response_seconds"`
	AverageRounds          float64  `json:"average_rounds"`
	ClosureRate            float64  `json:"closure_rate"`
	StabilityRate          *float64 `json:"stability_rate,omitempty"`
	ReboundRate            *float64 `json:"rebound_rate,omitempty"`
	OverdueRate            float64  `json:"overdue_rate"`
	DataSufficient         bool     `json:"data_sufficient"`
}
type ClosureStats struct {
	From                 time.Time        `json:"from"`
	To                   time.Time        `json:"to"`
	Area                 string           `json:"area,omitempty"`
	Metric               string           `json:"metric,omitempty"`
	RiskLevel            domain.RiskLevel `json:"risk_level,omitempty"`
	RecurrenceWindowDays int              `json:"recurrence_window_days"`
	GeneratedAt          time.Time        `json:"generated_at"`
	Results              []ClosureMetric  `json:"results"`
}

func (s *Service) ClosureStats(from, to time.Time, area, metric string, risk domain.RiskLevel, windowDays int) (ClosureStats, error) {
	if from.IsZero() || to.IsZero() || from.After(to) {
		return ClosureStats{}, &domain.ValidationError{Field: "time_range", Message: "from 不得晚于 to 且不能为空"}
	}
	if windowDays <= 0 || windowDays > 3650 {
		return ClosureStats{}, &domain.ValidationError{Field: "recurrence_window", Message: "复发窗口必须为 1 到 3650 天"}
	}
	type key struct{ a, m string }
	groups := map[key][]*domain.PreservationIncident{}
	for _, in := range s.Repo.List(domain.IncidentFilter{AreaID: area, RiskLevel: risk}) {
		if in.ObservedAt.Before(from) || in.ObservedAt.After(to) {
			continue
		}
		metrics := map[string]bool{}
		for _, r := range in.Readings {
			if r.Phase == domain.PhaseAbnormal && r.ReplacedByID == "" {
				metrics[r.Metric] = true
			}
		}
		for m := range metrics {
			if metric != "" && metric != m {
				continue
			}
			groups[key{in.AreaID, m}] = append(groups[key{in.AreaID, m}], in)
		}
	}
	out := ClosureStats{From: from, To: to, Area: area, Metric: metric, RiskLevel: risk, RecurrenceWindowDays: windowDays, GeneratedAt: s.now()}
	for k, items := range groups {
		sort.Slice(items, func(i, j int) bool { return items[i].ObservedAt.Before(items[j].ObservedAt) })
		var response, rounds float64
		closed, overdue := 0, 0
		stable, totalEffects, rebound := 0, 0, 0
		for idx, in := range items {
			for _, e := range in.Timeline {
				if e.EventType == "分派" {
					response += e.OccurredAt.Sub(in.ObservedAt).Seconds()
					break
				}
			}
			if in.Status == domain.StatusClosed {
				closed++
			}
			if in.DueAt.After(time.Time{}) && in.Status != domain.StatusClosed && s.now().After(in.DueAt) {
				overdue++
			}
			rounds += float64(len(in.Rounds))
			for _, st := range in.Stability {
				totalEffects++
				if st.Stable {
					stable++
				}
				if st.Rebounded {
					rebound++
				}
			}
			_ = idx
		}
		rec := 0
		for i := 1; i < len(items); i++ {
			if items[i].ObservedAt.Sub(items[i-1].ObservedAt) <= time.Duration(windowDays)*24*time.Hour {
				rec++
			}
		}
		m := ClosureMetric{Area: k.a, Metric: k.m, EventCount: len(items), RecurrenceCount: rec, AverageResponseSeconds: response / float64(maxInt(len(items), 1)), AverageRounds: rounds / float64(maxInt(len(items), 1)), ClosureRate: float64(closed) / float64(maxInt(len(items), 1)), OverdueRate: float64(overdue) / float64(maxInt(len(items), 1)), DataSufficient: totalEffects > 0}
		if totalEffects > 0 {
			v := float64(stable) / float64(totalEffects)
			m.StabilityRate = &v
			rv := float64(rebound) / float64(totalEffects)
			m.ReboundRate = &rv
		}
		out.Results = append(out.Results, m)
	}
	sort.Slice(out.Results, func(i, j int) bool {
		if out.Results[i].Area == out.Results[j].Area {
			return out.Results[i].Metric < out.Results[j].Metric
		}
		return out.Results[i].Area < out.Results[j].Area
	})
	return out, nil
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Service) RecommendAssignees(id string, revision int, assignees []string, due time.Time) (RecommendationPreview, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return RecommendationPreview{}, err
	}
	if in.Revision != revision {
		return RecommendationPreview{}, domain.ErrConflict
	}
	if due.IsZero() || due.Before(s.now()) {
		return RecommendationPreview{}, &domain.ValidationError{Field: "due_at", Message: "目标期限必须晚于当前时间"}
	}
	out := RecommendationPreview{IncidentID: id, Revision: revision, CreatedAt: s.now()}
	for _, a := range assignees {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		snap := s.workloadSnapshot(in, a, due, "")
		score := 100 - float64(snap.ActiveCount*10) - float64(len(snap.Conflicts)*25)
		reasons := []string{fmt.Sprintf("未关闭事件 %d 个", snap.ActiveCount)}
		for _, c := range snap.Conflicts {
			reasons = append(reasons, "期限冲突: "+c.IncidentID)
		}
		out.Recommendations = append(out.Recommendations, AssigneeRecommendation{Assignee: a, Score: score, Reasons: reasons, Conflicts: snap.Conflicts, Recommendable: len(snap.Conflicts) == 0})
	}
	sort.SliceStable(out.Recommendations, func(i, j int) bool {
		if out.Recommendations[i].Score == out.Recommendations[j].Score {
			return out.Recommendations[i].Assignee < out.Recommendations[j].Assignee
		}
		return out.Recommendations[i].Score > out.Recommendations[j].Score
	})
	out.Checksum = domain.AssignmentCandidatesChecksum(id, revision, nil)
	return out, nil
}

func (s *Service) ConfirmAssigneeRecommendation(id string, revision int, assignee string, due time.Time, checksum, summary string, items []domain.MitigationItem, actor, requestID, continueReason string) (*domain.PreservationIncident, error) {
	p, err := s.RecommendAssignees(id, revision, []string{assignee}, due)
	if err != nil {
		return nil, err
	}
	if checksum == "" || checksum != p.Checksum {
		return nil, &domain.ValidationError{Field: "recommendation_checksum", Message: "推荐快照已过期，请重新预览"}
	}
	return s.AssignWithContext(id, revision, assignee, due, summary, items, actor, requestID, "", continueReason)
}

func (s *Service) ReviewPreflight(id string, revision int) (domain.ReviewLock, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return domain.ReviewLock{}, err
	}
	if in.Revision != revision {
		return domain.ReviewLock{}, domain.ErrConflict
	}
	if in.Status != domain.StatusMitigating {
		return domain.ReviewLock{}, domain.ErrState
	}
	if in.Plan == nil || in.Plan.Progress < 1 {
		return domain.ReviewLock{}, &domain.ValidationError{Field: "review_evidence", Message: "仍有未完成措施"}
	}
	comps := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	ids := []string{}
	for _, c := range comps {
		if c.EffectReadingID != "" {
			ids = append(ids, c.EffectReadingID)
		}
	}
	if len(ids) != len(comps) {
		return domain.ReviewLock{}, &domain.ValidationError{Field: "review_evidence", Message: "每个异常指标都必须有有效效果读数"}
	}
	checksum := domain.AssignmentCandidatesChecksum(id, revision, nil)
	return domain.ReviewLock{Checksum: checksum, Revision: revision, Comparisons: comps, ReadingIDs: ids, LockedAt: s.now()}, nil
}

func (s *Service) SetReviewPreflight(id string, revision int) (*domain.PreservationIncident, error) {
	lock, err := s.ReviewPreflight(id, revision)
	if err != nil {
		return nil, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	in.SetReviewLock(lock)
	return in, nil
}

func (s *Service) ConfirmProcessEscalation(id string, revision int, note, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct{ N, A string }{note, actor})
	if old, ok, e := s.reuse(requestID, "escalation-resolve", id, digest); ok || e != nil {
		return old, e
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ResolveEscalation(revision, note, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "escalation-resolve", digest)
}
