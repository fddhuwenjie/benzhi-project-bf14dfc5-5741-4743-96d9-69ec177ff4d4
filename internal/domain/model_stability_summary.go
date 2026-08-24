package domain

import "time"

type StabilitySummary struct {
	Metric                string        `json:"metric"`
	Stable                bool          `json:"stable"`
	Rebounded             bool          `json:"rebounded"`
	MinimumWindow         time.Duration `json:"minimum_window"`
	ObservedSpan          time.Duration `json:"observed_span"`
	ParticipatingReadings []string      `json:"participating_reading_ids"`
	Trend                 []TrendPoint  `json:"trend"`
}
