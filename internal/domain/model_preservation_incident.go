package domain

import "time"

type PreservationIncident struct {
	ID                     string                       `json:"id"`
	AreaID                 string                       `json:"area_id"`
	AffectedScope          string                       `json:"affected_scope"`
	ObservedAt             time.Time                    `json:"observed_at"`
	Status                 Status                       `json:"status"`
	RiskLevel              RiskLevel                    `json:"risk_level"`
	RiskBasis              []string                     `json:"risk_basis"`
	ResponseDue            time.Duration                `json:"response_due"`
	AssessmentIntervals    []AbnormalInterval           `json:"assessment_intervals"`
	Evidence               []EvidenceSummary            `json:"evidence"`
	Assignee               string                       `json:"assignee,omitempty"`
	DueAt                  time.Time                    `json:"due_at,omitempty"`
	Deadline               DeadlineInfo                 `json:"deadline"`
	Revision               int                          `json:"revision"`
	CreatedAt              time.Time                    `json:"created_at"`
	UpdatedAt              time.Time                    `json:"updated_at"`
	Sensitivity            string                       `json:"sensitivity"`
	Readings               []EnvironmentalReading       `json:"readings"`
	Plan                   *MitigationPlan              `json:"plan,omitempty"`
	Verification           *Verification                `json:"verification,omitempty"`
	Rounds                 []TreatmentRound             `json:"rounds"`
	CurrentRound           int                          `json:"current_round"`
	ClosedRound            int                          `json:"closed_round,omitempty"`
	Comparisons            []ReadingComparison          `json:"comparisons,omitempty"`
	BaselinePairings       []BaselinePairing            `json:"baseline_pairings"`
	MissingBaselines       []string                     `json:"missing_baseline_metrics"`
	RuleSetVersion         string                       `json:"rule_set_version"`
	RuleHits               []RuleHit                    `json:"rule_hits"`
	RuleSnapshot           RuleSnapshot                 `json:"rule_snapshot"`
	RuleTemplateVersion    string                       `json:"rule_template_version,omitempty"`
	PendingManualReview    bool                         `json:"pending_manual_review,omitempty"`
	ManualReviewMissing    []string                     `json:"manual_review_missing,omitempty"`
	Stability              []StabilitySummary           `json:"stability,omitempty"`
	RetestMetrics          []string                     `json:"retest_metrics,omitempty"`
	AssigneeTransfers      []AssigneeTransfer           `json:"assignee_transfers,omitempty"`
	TreatmentOverdue       bool                         `json:"treatment_overdue"`
	Timeline               []IncidentEvent              `json:"timeline"`
	RelatedCandidates      []IncidentCandidate          `json:"related_candidates,omitempty"`
	IndependentReason      string                       `json:"independent_reason,omitempty"`
	Archive                *ArchiveSummary              `json:"archive,omitempty"`
	TimelinePage           *TimelinePage                `json:"timeline_page,omitempty"`
	AffectedItems          []AffectedCollectionItem     `json:"affected_items,omitempty"`
	SensitivityTriggers    []string                     `json:"sensitivity_trigger_item_ids,omitempty"`
	AdditionalObservations []SupplementalObservation    `json:"additional_observations,omitempty"`
	AssignmentCandidates   []AssignmentCandidateSummary `json:"assignment_candidates,omitempty"`
	PlanChanges            []PlanChangeAudit            `json:"plan_changes,omitempty"`
	PendingDeadlineChange  *DeadlineChangeRequest       `json:"pending_deadline_change,omitempty"`
	DeadlineChangeHistory  []DeadlineChangeRequest      `json:"deadline_change_history,omitempty"`
	DeadlineChangeCount    int                          `json:"deadline_change_count"`
	Escalation             *ProcessEscalation           `json:"escalation,omitempty"`
	ReviewLock             *ReviewLock                  `json:"review_lock,omitempty"`
	RetestCheckpoints      []RetestCheckpoint           `json:"retest_checkpoints,omitempty"`
	NextRetestAt           *time.Time                   `json:"next_retest_at,omitempty"`
	PendingRetests         []string                     `json:"pending_retests,omitempty"`
	CompletedRetests       []string                     `json:"completed_retests,omitempty"`
	MissedRetests          []string                     `json:"missed_retests,omitempty"`
	AcceptanceStandards    []AcceptanceStandard         `json:"acceptance_standards,omitempty"`
	HandoverSignatures     []HandoverSignature          `json:"handover_signatures,omitempty"`
}
