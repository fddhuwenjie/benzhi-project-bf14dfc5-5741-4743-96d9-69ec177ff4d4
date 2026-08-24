package domain

import "time"

type ResponsibilityCheck struct {
	Reviewer  string    `json:"reviewer"`
	Assignor  string    `json:"assignor"`
	Assignee  string    `json:"assignee"`
	Recorders []string  `json:"recorders"`
	Separated bool      `json:"separated"`
	CheckedAt time.Time `json:"checked_at"`
}
