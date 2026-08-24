package domain

import "time"

type EvidenceSummary struct {
	ReadingID  string    `json:"reading_id"`
	Metric     string    `json:"metric"`
	Reference  string    `json:"reference"`
	SourceNote string    `json:"source_note"`
	RecordedAt time.Time `json:"recorded_at"`
}
