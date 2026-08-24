package domain

import "time"

// RetestCheckpoint 是措施项内的复测门禁。
type RetestCheckpoint struct {
	ID               string        `json:"id"`
	ItemID           string        `json:"item_id"`
	Metric           string        `json:"metric"`
	PlannedAt        time.Time     `json:"planned_at"`
	AllowedDeviation time.Duration `json:"allowed_deviation"`
	Required         bool          `json:"required"`
	Status           string        `json:"status"`
	ReadingID        string        `json:"reading_id,omitempty"`
	EvidenceRef      string        `json:"evidence_ref,omitempty"`
	CompletedAt      *time.Time    `json:"completed_at,omitempty"`
	MissReason       string        `json:"miss_reason,omitempty"`
}
