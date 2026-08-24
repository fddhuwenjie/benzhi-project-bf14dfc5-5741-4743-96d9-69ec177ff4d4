package domain

import "time"

type TrendPoint struct {
	ReadingID       string    `json:"reading_id"`
	MeasuredAt      time.Time `json:"measured_at"`
	Value           float64   `json:"value"`
	Unit            string    `json:"unit"`
	ChangeFromPrev  *float64  `json:"change_from_previous,omitempty"`
	Recovery        float64   `json:"recovery_percent"`
	WithinThreshold bool      `json:"within_threshold"`
}
