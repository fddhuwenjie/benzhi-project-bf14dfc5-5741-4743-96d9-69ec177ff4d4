package domain

import "time"

type MitigationPlan struct {
	ID          string           `json:"id"`
	IncidentID  string           `json:"incident_id"`
	Summary     string           `json:"summary"`
	Owner       string           `json:"owner"`
	Items       []MitigationItem `json:"items"`
	DueAt       time.Time        `json:"due_at"`
	SubmittedAt *time.Time       `json:"submitted_at,omitempty"`
	CompletedAt *time.Time       `json:"completed_at,omitempty"`
	Round       int              `json:"round"`
	OverdueNote string           `json:"overdue_note,omitempty"`
	Workload    WorkloadSnapshot `json:"workload_snapshot"`
	Progress    float64          `json:"completion_ratio"`
}
