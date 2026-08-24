package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	Repo        domain.Repository
	Rules       assessment.RuleSet
	Now         func() time.Time
	listCacheMu sync.RWMutex
	listCache   map[string][]byte
}

type auditRepository interface {
	AuditEvents(string) ([]domain.IncidentEvent, error)
}

type CreateCommand struct {
	ID, AreaID, AffectedScope, Sensitivity, ThresholdTemplateVersion, Actor, RequestID string
	IndependentReason                                                                  string
	ObservedAt, SubmittedAt                                                            time.Time
	Readings                                                                           []domain.EnvironmentalReading
	AffectedItems                                                                      []domain.AffectedCollectionItem
}

type ListResult struct {
	Incidents  []*domain.PreservationIncident `json:"incidents"`
	Statistics IncidentStatistics             `json:"statistics"`
}

type IncidentStatistics struct {
	Total             int                      `json:"total"`
	MatchingIncidents int                      `json:"matching_incident_count"`
	AffectedItemRows  int                      `json:"affected_item_rows"`
	AffectedQuantity  int                      `json:"affected_total_quantity"`
	ByMaterial        map[string]int           `json:"by_material"`
	ByStatus          map[domain.Status]int    `json:"by_status"`
	ByRiskLevel       map[domain.RiskLevel]int `json:"by_risk_level"`
	PendingOverdue    int                      `json:"pending_overdue"`
	MitigatingOverdue int                      `json:"mitigating_overdue"`
	DueSoon           int                      `json:"due_soon"`
	GeneratedAt       time.Time                `json:"generated_at"`
	Filters           domain.IncidentFilter    `json:"filters"`
	ByArea            []DimensionStatistic     `json:"by_area"`
	StatusDimensions  []DimensionStatistic     `json:"status_dimensions"`
	RiskDimensions    []DimensionStatistic     `json:"risk_dimensions"`
}

type DimensionStatistic struct {
	Key                    string  `json:"key"`
	Count                  int     `json:"count"`
	OverdueCount           int     `json:"overdue_count"`
	AverageResponseSeconds float64 `json:"average_response_seconds"`
	ClosureRate            float64 `json:"closure_rate"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Create(c CreateCommand) (*domain.PreservationIncident, error) {
	if c.SubmittedAt.IsZero() {
		c.SubmittedAt = s.now()
	}
	digest := requestDigest(struct {
		ID, AreaID, Scope, Sensitivity, Template, Actor string
		ObservedAt                                      time.Time
		Readings                                        []domain.EnvironmentalReading
		IndependentReason                               string
		AffectedItems                                   []domain.AffectedCollectionItem
	}{c.ID, c.AreaID, c.AffectedScope, c.Sensitivity, c.ThresholdTemplateVersion, c.Actor, c.ObservedAt, c.Readings, c.IndependentReason, c.AffectedItems})
	if in, handled, err := s.reuse(c.RequestID, "create", c.ID, digest); handled || err != nil {
		return in, err
	}
	preview := s.Preflight(c)
	if len(preview.Errors) > 0 {
		issue := preview.Errors[0]
		return nil, &domain.ValidationError{Field: issue.Field, Message: issue.Message}
	}
	items, scope, triggers, err := domain.ValidateAffectedItems(c.AffectedItems, c.Sensitivity)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		c.AffectedScope = scope
	}
	result := assessment.Result{Level: preview.RiskLevel, Basis: preview.RiskBasis, Response: preview.ResponseDue, Normalized: preview.NormalizedReadings, Intervals: preview.Intervals, Pairings: preview.BaselinePairings, MissingBaselines: preview.MissingBaselines, RuleVersion: preview.RuleSetVersion, RuleHits: preview.RuleHits}
	candidates := s.sourceCandidates(c, result.Normalized)
	var activeExact, activeRelated []domain.IncidentCandidate
	for _, candidate := range candidates {
		if candidate.Historical {
			continue
		}
		if candidate.Match == "完全重复" {
			activeExact = append(activeExact, candidate)
		} else {
			activeRelated = append(activeRelated, candidate)
		}
	}
	if len(activeExact) > 0 {
		return nil, &domain.CandidateConflictError{Kind: "exact_duplicate", Candidates: activeExact, Message: "存在完全重复的活动事件"}
	}
	if len(activeRelated) > 0 && strings.TrimSpace(c.IndependentReason) == "" {
		return nil, &domain.CandidateConflictError{Kind: "related_confirmation_required", Candidates: activeRelated, Message: "存在可能关联的活动事件，需填写独立登记理由"}
	}
	if len([]rune(strings.TrimSpace(c.IndependentReason))) > 500 {
		return nil, &domain.ValidationError{Field: "independent_reason", Message: "独立登记理由不得超过 500 个字符"}
	}
	for n := range result.Normalized {
		result.Normalized[n].IncidentID = c.ID
	}
	in, err := domain.NewIncident(c.ID, c.AreaID, c.AffectedScope, c.Sensitivity, c.ObservedAt, result.Normalized, result.Level, result.Basis, result.Response)
	if err != nil {
		return nil, err
	}
	in.SetRegistrationDetails(result.Intervals)
	effectiveRules := s.Rules
	if c.ThresholdTemplateVersion != "" {
		if resolved, resolveErr := assessment.ResolveTemplate(effectiveRules, c.Sensitivity, c.ThresholdTemplateVersion); resolveErr == nil {
			effectiveRules = resolved
		}
	}
	in.SetAssessmentSnapshot(result.Pairings, result.MissingBaselines, result.RuleVersion, result.RuleHits, ruleSnapshot(effectiveRules))
	in.RuleTemplateVersion = c.ThresholdTemplateVersion
	in.RuleSnapshot.Version = result.RuleVersion
	in.RuleSnapshot.Sensitivity = c.Sensitivity
	if missing := credibilityMissing(result.Level, result.Normalized); len(missing) > 0 {
		in.PendingManualReview, in.ManualReviewMissing = true, missing
		in.AppendManualReviewEvent(c.Actor, c.RequestID, missing)
	}
	in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, effectiveRules))
	in.SetRegistrationActor(c.Actor, c.RequestID)
	in.SetRegistrationContext(candidates, strings.TrimSpace(c.IndependentReason))
	in.SetAffectedItems(items, triggers)
	if len(triggers) > 0 {
		in.RiskBasis = append(in.RiskBasis, "最高敏感级别藏品: "+strings.Join(triggers, "、"))
		if len(in.Timeline) > 0 {
			in.Timeline[0].Payload["risk_basis"] = in.RiskBasis
		}
	}
	rec := domain.RequestRecord{RequestID: c.RequestID, Operation: "create", IncidentID: c.ID, Digest: digest, SuccessRevision: in.Revision, Result: in}
	if err = s.Repo.Commit(in, 0, rec); err != nil {
		return nil, err
	}
	stored, _ := s.Repo.FindRequest(c.RequestID)
	return stored.Result, nil
}

func credibilityMissing(level domain.RiskLevel, readings []domain.EnvironmentalReading) []string {
	if level != domain.RiskHigh && level != domain.RiskCritical {
		return nil
	}
	refs := map[string]bool{}
	low := false
	metadata := false
	for _, r := range readings {
		if r.Phase != domain.PhaseAbnormal {
			continue
		}
		refs[r.EvidenceRef] = true
		if r.Credibility != "" || r.CredibilityLevel != "" || r.CollectionSource != "" || r.Source != "" || r.CalibrationStatus != "" || r.CalibrationState != "" {
			metadata = true
		}
		if strings.EqualFold(r.Credibility, "低") || strings.EqualFold(r.CredibilityLevel, "低") || r.CalibrationStatus == "失效" || r.CalibrationState == "失效" {
			low = true
		}
	}
	if !metadata {
		return nil
	}
	var missing []string
	if len(refs) < 2 {
		missing = append(missing, "独立读数")
	}
	if low {
		missing = append(missing, "校准状态")
	}
	return missing
}

func (s *Service) Assign(id string, rev int, assignee string, due time.Time, summary string, items []domain.MitigationItem, actor, req string) (*domain.PreservationIncident, error) {
	return s.AssignWithOverdue(id, rev, assignee, due, summary, items, actor, req, "")
}

func (s *Service) AssignWithOverdue(id string, rev int, assignee string, due time.Time, summary string, items []domain.MitigationItem, actor, req, overdueNote string) (*domain.PreservationIncident, error) {
	return s.AssignWithContext(id, rev, assignee, due, summary, items, actor, req, overdueNote, "")
}

func (s *Service) AssignWithContext(id string, rev int, assignee string, due time.Time, summary string, items []domain.MitigationItem, actor, req, overdueNote, continueReason string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Assignee, Summary, Actor, OverdueNote, ContinueReason string
		DueAt                                                 time.Time
		Items                                                 []domain.MitigationItem
	}{assignee, summary, actor, overdueNote, continueReason, due, items})
	if in, handled, err := s.reuse(req, "assignment", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Revision != rev {
		return nil, domain.ErrConflict
	}
	snapshot := s.workloadSnapshot(in, assignee, due, strings.TrimSpace(continueReason))
	if len(snapshot.Conflicts) > 0 && strings.TrimSpace(continueReason) == "" {
		return nil, &domain.WorkloadConflictError{Snapshot: snapshot, Message: "执行人的活动任务期限发生冲突，需改派或填写继续分派说明"}
	}
	if len([]rune(strings.TrimSpace(continueReason))) > 500 {
		return nil, &domain.ValidationError{Field: "continue_reason", Message: "继续分派说明不得超过 500 个字符"}
	}
	plan := domain.MitigationPlan{Summary: strings.TrimSpace(summary), Items: items, Owner: assignee, DueAt: due, Workload: snapshot}
	if err = in.AssignWithDeadline(rev, assignee, due, plan, actor, req, overdueNote, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, rev, req, "assignment", digest)
}

func (s *Service) ConfirmManualReview(id string, rev int, approve bool, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Approve bool
		Actor   string
	}{approve, actor})
	if in, handled, err := s.reuse(req, "manual-review", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ConfirmManualReview(rev, approve, actor, req, s.now()); err != nil {
		if !approve && len(in.Timeline) > 0 {
			_, _ = s.commit(in, rev, req, "manual-review", digest)
		}
		return nil, err
	}
	return s.commit(in, rev, req, "manual-review", digest)
}

func (s *Service) Record(id string, rev int, item, note, effect, evidence, actor, req string) (*domain.PreservationIncident, error) {
	now := s.now()
	v := 0.0
	fmt.Sscanf(effect, "%f", &v)
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	metric, unit := "温度", "℃"
	for _, r := range in.Readings {
		if r.Phase == domain.PhaseAbnormal {
			metric, unit = r.Metric, r.Unit
			break
		}
	}
	r := domain.EnvironmentalReading{Metric: metric, Value: v, Unit: unit, MeasuredAt: now, SourceNote: note, EvidenceRef: evidence, EvidenceRecordedAt: now}
	return s.RecordReadings(id, rev, item, note, []domain.EnvironmentalReading{r}, actor, req)
}

func (s *Service) RecordReadings(id string, rev int, item, note string, readings []domain.EnvironmentalReading, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Item, Note, Actor string
		Readings          []domain.EnvironmentalReading
	}{item, note, actor, readings})
	if in, handled, err := s.reuse(req, "items", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	normalized, err := s.normalizeEffects(in, readings)
	if err != nil {
		return nil, err
	}
	if err = in.RecordItemReadings(rev, item, note, normalized, actor, req, s.now()); err != nil {
		return nil, err
	}
	rules := lockedRules(in, s.Rules)
	in.Comparisons = assessment.Compare(in.Readings, rules)
	in.ApplyStability(currentRoundStability(in, rules))
	for _, st := range in.Stability {
		if st.Rebounded {
			ids := append([]string(nil), st.ParticipatingReadings...)
			in.Escalation = &domain.ProcessEscalation{ItemID: item, Metrics: []string{st.Metric}, TriggerReadingIDs: ids, Reason: "效果读数重新越界", SuggestedRetestAt: s.now().Add(2 * time.Hour)}
			for n := range in.Plan.Items {
				if in.Plan.Items[n].ID == item {
					in.Plan.Items[n].Status = "需整改"
					in.Plan.Items[n].Executable = false
					in.Plan.Items[n].PauseReason = "效果读数重新越界"
					in.Plan.Items[n].PausedAt = func() *time.Time { t := s.now(); return &t }()
				} else {
					for _, dep := range in.Plan.Items[n].PrerequisiteIDs {
						if dep == item && in.Plan.Items[n].Status != "已完成" {
							in.Plan.Items[n].BlockedBy = []string{item}
							in.Plan.Items[n].Executable = false
						}
					}
				}
			}
			in.AppendEscalationEvent(actor, req, st.Metric, ids)
			break
		}
	}
	return s.commit(in, rev, req, "items", digest)
}

func (s *Service) PauseItem(id string, rev int, item, reason string, startedAt, resumeAt time.Time, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Item, Reason, Actor string
		StartedAt, ResumeAt time.Time
	}{item, reason, actor, startedAt, resumeAt})
	if in, handled, err := s.reuse(req, "pause", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.PauseItem(rev, item, reason, startedAt, resumeAt, actor, req, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, rev, req, "pause", digest)
}

func (s *Service) ResumeItem(id string, rev int, item string, resumedAt time.Time, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Item, Actor string
		ResumedAt   time.Time
	}{item, actor, resumedAt})
	if in, handled, err := s.reuse(req, "resume", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.ResumeItem(rev, item, resumedAt, actor, req, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, rev, req, "resume", digest)
}

func (s *Service) Submit(id string, rev int, actor, req string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct{ Actor string }{actor})
	if in, handled, err := s.reuse(req, "submit-review", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	comparisons := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	if err = in.SubmitReviewWithComparisonsAt(rev, actor, req, comparisons, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, rev, req, "submit-review", digest)
}

func (s *Service) Verify(id string, rev int, reviewer, decision, reason, req string) (*domain.PreservationIncident, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	comparisons := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	return s.VerifyConfirmed(id, rev, reviewer, decision, reason, req, comparisonReadingIDs(comparisons))
}

// SubmitWithReviewLock enforces the comparison set produced by review preflight.
func (s *Service) SubmitWithReviewLock(id string, rev int, actor, req, checksum string, confirmed []string) (*domain.PreservationIncident, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.ReviewLock == nil {
		lock, e := s.ReviewPreflight(id, rev)
		if e != nil {
			return nil, e
		}
		in.ReviewLock = &lock
	}
	if in.ReviewLock.Revision != rev || in.ReviewLock.Checksum != checksum {
		return nil, &domain.ValidationError{Field: "comparison_checksum", Message: "复核证据包已过期，请重新预检"}
	}
	ids := append([]string(nil), in.ReviewLock.ReadingIDs...)
	sort.Strings(ids)
	got := append([]string(nil), confirmed...)
	sort.Strings(got)
	if len(ids) != len(got) {
		return nil, &domain.ValidationError{Field: "confirmed_reading_ids", Message: "确认读数集合与锁定比较集不一致"}
	}
	for n := range ids {
		if ids[n] != got[n] {
			return nil, &domain.ValidationError{Field: "confirmed_reading_ids", Message: "确认读数集合与锁定比较集不一致"}
		}
	}
	digest := requestDigest(struct {
		Checksum string
		IDs      []string
	}{checksum, confirmed})
	if old, ok, e := s.reuse(req, "submit-review-lock", id, digest); ok || e != nil {
		return old, e
	}
	comparisons := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	if err = in.SubmitReviewWithComparisonsAt(rev, actor, req, comparisons, s.now()); err != nil {
		return nil, err
	}
	in.Timeline[len(in.Timeline)-1].Payload["comparison_checksum"] = checksum
	in.ReviewLock = nil
	return s.commit(in, rev, req, "submit-review-lock", digest)
}

func (s *Service) VerifyConfirmed(id string, rev int, reviewer, decision, reason, req string, confirmedIDs []string) (*domain.PreservationIncident, error) {
	return s.VerifyConfirmedCategory(id, rev, reviewer, decision, "", reason, req, confirmedIDs)
}

func (s *Service) VerifyConfirmedCategory(id string, rev int, reviewer, decision, category, reason, req string, confirmedIDs []string) (*domain.PreservationIncident, error) {
	digest := requestDigest(struct {
		Reviewer, Decision, Reason string
		Confirmed                  []string
	}{reviewer, decision, reason, confirmedIDs})
	if in, handled, err := s.reuse(req, "verification", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	comparisons := assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	now := s.now()
	if category != "" {
		err = in.VerifyWithCategory(rev, reviewer, decision, category, reason, req, comparisons, confirmedIDs, now)
	} else {
		err = in.VerifyConfirmedWithComparisonsAt(rev, reviewer, decision, reason, req, comparisons, confirmedIDs, now)
	}
	if err != nil {
		return nil, err
	}
	if in.Status == domain.StatusClosed {
		if err = in.FreezeArchive(now); err != nil {
			return nil, err
		}
	}
	return s.commit(in, rev, req, "verification", digest)
}

func (s *Service) Get(id string) (*domain.PreservationIncident, error) {
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	in.RefreshDeadline(now)
	in.TreatmentOverdue = in.Status == domain.StatusMitigating && !in.DueAt.IsZero() && now.After(in.DueAt)
	in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	in.RefreshPlanState()
	in.VerifyArchive()
	events := s.Repo.AllEvents(id)
	if repo, ok := s.Repo.(auditRepository); ok {
		events, err = repo.AuditEvents(id)
		if err != nil {
			return nil, err
		}
	}
	in.Timeline = domain.DecorateTimeline(events)
	return in, nil
}

func (s *Service) List(filter domain.IncidentFilter) ListResult {
	cacheKey := requestDigest(filter)
	items, cached := s.loadListProjection(cacheKey)
	if !cached {
		items = s.Repo.List(filter)
		if len(items) >= 64 {
			s.storeListProjection(cacheKey, items)
		}
	}
	now := s.now()
	items = filterDeadlineBucket(items, filter.DeadlineBucket, now)
	stats := IncidentStatistics{
		ByStatus:    map[domain.Status]int{domain.StatusPending: 0, domain.StatusMitigating: 0, domain.StatusReview: 0, domain.StatusClosed: 0},
		ByRiskLevel: map[domain.RiskLevel]int{domain.RiskLow: 0, domain.RiskMedium: 0, domain.RiskHigh: 0, domain.RiskCritical: 0},
		ByMaterial:  map[string]int{},
		GeneratedAt: now, Filters: filter,
	}
	for _, in := range items {
		in.RefreshDeadline(now)
		in.RefreshRetestSummary(now)
		in.TreatmentOverdue = in.Status == domain.StatusMitigating && !in.DueAt.IsZero() && now.After(in.DueAt)
		in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, s.Rules))
		stats.Total++
		stats.MatchingIncidents++
		stats.ByStatus[in.Status]++
		stats.ByRiskLevel[in.RiskLevel]++
		for _, affected := range in.AffectedItems {
			if !filter.MatchesAffectedItem(affected) {
				continue
			}
			stats.AffectedItemRows++
			stats.AffectedQuantity += affected.Quantity
			stats.ByMaterial[affected.Material] += affected.Quantity
		}
		deadline := in.ObservedAt.Add(in.ResponseDue)
		if in.Status == domain.StatusMitigating {
			deadline = in.DueAt
		}
		if in.Status == domain.StatusPending && now.After(deadline) {
			stats.PendingOverdue++
		}
		if in.Status == domain.StatusMitigating && now.After(deadline) {
			stats.MitigatingOverdue++
		}
		if in.Status != domain.StatusClosed && !now.After(deadline) && deadline.Sub(now) <= 24*time.Hour {
			stats.DueSoon++
		}
	}
	stats.ByArea = dimensionStatistics(items, "area", nil, now)
	if len(stats.ByArea) == 0 && filter.AreaID != "" {
		stats.ByArea = []DimensionStatistic{{Key: filter.AreaID}}
	}
	stats.StatusDimensions = dimensionStatistics(items, "status", []string{string(domain.StatusPending), string(domain.StatusMitigating), string(domain.StatusReview), string(domain.StatusClosed)}, now)
	stats.RiskDimensions = dimensionStatistics(items, "risk", []string{string(domain.RiskCritical), string(domain.RiskHigh), string(domain.RiskMedium), string(domain.RiskLow)}, now)
	sortIncidents(items, now)
	return ListResult{Incidents: items, Statistics: stats}
}

func (s *Service) loadListProjection(key string) ([]*domain.PreservationIncident, bool) {
	s.listCacheMu.RLock()
	payload, ok := s.listCache[key]
	s.listCacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	var incidents []*domain.PreservationIncident
	if json.Unmarshal(payload, &incidents) != nil {
		return nil, false
	}
	return incidents, true
}

func (s *Service) storeListProjection(key string, incidents []*domain.PreservationIncident) {
	payload, err := json.Marshal(incidents)
	if err != nil {
		return
	}
	s.listCacheMu.Lock()
	defer s.listCacheMu.Unlock()
	if s.listCache == nil {
		s.listCache = make(map[string][]byte)
	}
	s.listCache[key] = payload
}

func filterDeadlineBucket(items []*domain.PreservationIncident, bucket string, now time.Time) []*domain.PreservationIncident {
	if bucket == "" {
		return items
	}
	filtered := make([]*domain.PreservationIncident, 0, len(items))
	for _, in := range items {
		if bucket == "retest_due" || bucket == "retest_overdue" {
			matched := false
			for _, checkpoint := range in.RetestCheckpoints {
				if checkpoint.Status == "待复测" {
					overdue := now.After(checkpoint.PlannedAt.Add(checkpoint.AllowedDeviation))
					if bucket == "retest_overdue" && overdue || bucket == "retest_due" && !overdue && checkpoint.PlannedAt.Before(now.Add(24*time.Hour)) {
						matched = true
						break
					}
				}
			}
			if matched {
				filtered = append(filtered, in)
			}
			continue
		}
		deadline := in.ObservedAt.Add(in.ResponseDue)
		if in.Status == domain.StatusMitigating {
			deadline = in.DueAt
		}
		match := bucket == "pending_overdue" && in.Status == domain.StatusPending && now.After(deadline) ||
			bucket == "mitigating_overdue" && in.Status == domain.StatusMitigating && now.After(deadline) ||
			bucket == "due_soon" && in.Status != domain.StatusClosed && !now.After(deadline) && deadline.Sub(now) <= 24*time.Hour
		if match {
			filtered = append(filtered, in)
		}
	}
	return filtered
}

func (s *Service) reuse(requestID, operation, incidentID, digest string) (*domain.PreservationIncident, bool, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, false, &domain.ValidationError{Field: "request_id", Message: "请求标识不能为空"}
	}
	rec, ok := s.Repo.FindRequest(requestID)
	if !ok {
		return nil, false, nil
	}
	if rec.Operation == operation && rec.IncidentID == incidentID && rec.Digest == digest {
		if rec.Result != nil {
			return rec.Result, true, nil
		}
		in, err := s.Repo.Get(rec.IncidentID)
		return in, true, err
	}
	current, _ := s.Repo.Get(rec.IncidentID)
	conflict := &domain.IdempotencyConflictError{IncidentID: rec.IncidentID}
	if current != nil {
		conflict.Status, conflict.Revision = current.Status, current.Revision
	}
	return nil, true, conflict
}

func (s *Service) commit(in *domain.PreservationIncident, expected int, requestID, operation, digest string) (*domain.PreservationIncident, error) {
	rec := domain.RequestRecord{RequestID: requestID, Operation: operation, IncidentID: in.ID, Digest: digest, SuccessRevision: in.Revision, Result: in}
	if err := s.Repo.Commit(in, expected, rec); err != nil {
		return nil, err
	}
	stored, _ := s.Repo.FindRequest(requestID)
	return stored.Result, nil
}

func requestDigest(v interface{}) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sortIncidents(items []*domain.PreservationIncident, now time.Time) {
	risk := map[domain.RiskLevel]int{domain.RiskCritical: 4, domain.RiskHigh: 3, domain.RiskMedium: 2, domain.RiskLow: 1}
	deadline := func(in *domain.PreservationIncident) time.Time {
		if in.Status == domain.StatusMitigating && !in.DueAt.IsZero() {
			return in.DueAt
		}
		return in.ObservedAt.Add(in.ResponseDue)
	}
	sort.SliceStable(items, func(a, b int) bool {
		if risk[items[a].RiskLevel] != risk[items[b].RiskLevel] {
			return risk[items[a].RiskLevel] > risk[items[b].RiskLevel]
		}
		aOverdue, bOverdue := deadline(items[a]).Before(now), deadline(items[b]).Before(now)
		if aOverdue != bOverdue {
			return aOverdue
		}
		if !deadline(items[a]).Equal(deadline(items[b])) {
			return deadline(items[a]).Before(deadline(items[b]))
		}
		return items[a].ID < items[b].ID
	})
}

func ParseItems(raw []string) []domain.MitigationItem {
	out := make([]domain.MitigationItem, 0, len(raw))
	for n, v := range raw {
		out = append(out, domain.MitigationItem{ID: fmt.Sprintf("item-%d", n+1), Description: v, Status: "待执行"})
	}
	return out
}
