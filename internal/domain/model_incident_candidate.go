package domain

import "time"

type IncidentCandidate struct {
	IncidentID string    `json:"incident_id"`
	Status     Status    `json:"status"`
	Revision   int       `json:"revision"`
	ObservedAt time.Time `json:"observed_at"`
	Match      string    `json:"match"`
	Historical bool      `json:"historical"`
}
