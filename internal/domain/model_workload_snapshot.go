package domain

import "time"

type WorkloadSnapshot struct {
	Assignee       string          `json:"assignee"`
	CapturedAt     time.Time       `json:"captured_at"`
	ActiveCount    int             `json:"active_count"`
	Conflicts      []WorkloadEvent `json:"conflicts"`
	ContinueReason string          `json:"continue_reason,omitempty"`
}
