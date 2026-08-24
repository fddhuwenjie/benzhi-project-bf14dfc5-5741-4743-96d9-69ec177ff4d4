package domain

import "time"

type ArchiveRoundSummary struct {
	Round          int              `json:"round"`
	PlanID         string           `json:"plan_id"`
	Assignee       string           `json:"assignee"`
	DueAt          time.Time        `json:"due_at"`
	Items          []MitigationItem `json:"items"`
	Verification   *Verification    `json:"verification,omitempty"`
	ReturnedReason string           `json:"returned_reason,omitempty"`
}
