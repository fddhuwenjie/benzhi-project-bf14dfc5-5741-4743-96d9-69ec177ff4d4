package domain

import "time"

type WorkloadEvent struct {
	IncidentID string    `json:"incident_id"`
	RiskLevel  RiskLevel `json:"risk_level"`
	Status     Status    `json:"status"`
	Revision   int       `json:"revision"`
	DueAt      time.Time `json:"due_at"`
}
