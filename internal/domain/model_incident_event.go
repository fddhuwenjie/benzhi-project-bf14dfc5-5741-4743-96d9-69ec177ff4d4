package domain

import "time"

type IncidentEvent struct {
	ID             string                 `json:"id"`
	IncidentID     string                 `json:"incident_id"`
	Sequence       int                    `json:"sequence"`
	EventType      string                 `json:"event_type"`
	Actor          string                 `json:"actor"`
	OccurredAt     time.Time              `json:"occurred_at"`
	Payload        map[string]interface{} `json:"payload,omitempty"`
	RequestID      string                 `json:"request_id,omitempty"`
	Round          int                    `json:"round,omitempty"`
	StatusBefore   Status                 `json:"status_before,omitempty"`
	StatusAfter    Status                 `json:"status_after,omitempty"`
	RevisionBefore int                    `json:"revision_before"`
	RevisionAfter  int                    `json:"revision_after"`
	ObjectID       string                 `json:"object_id,omitempty"`
}
