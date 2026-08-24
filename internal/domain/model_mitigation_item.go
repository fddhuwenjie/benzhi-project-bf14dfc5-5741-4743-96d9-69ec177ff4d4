package domain

import "time"

type MitigationItem struct {
	ID                 string             `json:"id"`
	Description        string             `json:"description"`
	Status             string             `json:"status"`
	Note               string             `json:"note,omitempty"`
	EffectReadingIDs   []string           `json:"effect_reading_ids,omitempty"`
	Evidence           string             `json:"evidence,omitempty"`
	CompletedAt        *time.Time         `json:"completed_at,omitempty"`
	PrerequisiteIDs    []string           `json:"prerequisite_ids,omitempty"`
	Executable         bool               `json:"executable"`
	BlockedBy          []string           `json:"blocked_by,omitempty"`
	CorrectionCount    int                `json:"correction_count,omitempty"`
	Stability          []StabilitySummary `json:"stability,omitempty"`
	PausedAt           *time.Time         `json:"paused_at,omitempty"`
	PauseReason        string             `json:"pause_reason,omitempty"`
	ExpectedResumeAt   *time.Time         `json:"expected_resume_at,omitempty"`
	CoveredMetrics     []string           `json:"covered_metrics,omitempty"`
	ProcessRecords     []ProcessRecord    `json:"process_records,omitempty"`
	ProcessTrends      []ProcessTrend     `json:"process_trends,omitempty"`
	CancelledAt        *time.Time         `json:"cancelled_at,omitempty"`
	CancellationReason string             `json:"cancellation_reason,omitempty"`
}
