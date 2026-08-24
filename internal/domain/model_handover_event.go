package domain

import "time"

type HandoverEvent struct {
	IncidentID  string    `json:"incident_id"`
	Revision    int       `json:"revision"`
	Status      Status    `json:"status"`
	RiskLevel   RiskLevel `json:"risk_level"`
	Assignee    string    `json:"assignee,omitempty"`
	DueAt       time.Time `json:"due_at,omitempty"`
	NextAction  string    `json:"next_action"`
	BlockReason string    `json:"block_reason,omitempty"`
}
