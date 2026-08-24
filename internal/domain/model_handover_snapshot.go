package domain

import "time"

type HandoverSnapshot struct {
	ID         string          `json:"id"`
	Shift      string          `json:"shift"`
	From       string          `json:"from"`
	To         string          `json:"to"`
	Filters    IncidentFilter  `json:"filters"`
	CapturedAt time.Time       `json:"captured_at"`
	Checksum   string          `json:"checksum"`
	Events     []HandoverEvent `json:"events"`
	SignedAt   *time.Time      `json:"signed_at,omitempty"`
}
