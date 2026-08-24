package domain

import "time"

type AcceptanceStandard struct {
	Metric              string        `json:"metric"`
	TargetMin           *float64      `json:"target_min,omitempty"`
	TargetMax           *float64      `json:"target_max,omitempty"`
	Unit                string        `json:"unit"`
	MinimumStableFor    time.Duration `json:"minimum_stable_for"`
	Deadline            time.Time     `json:"deadline"`
	EvidenceRequirement string        `json:"evidence_requirement"`
	Round               int           `json:"round"`
}
