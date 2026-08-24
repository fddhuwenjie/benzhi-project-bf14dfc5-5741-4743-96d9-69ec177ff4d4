package domain

import "time"

type AbnormalInterval struct {
	Metric     string        `json:"metric"`
	StartedAt  time.Time     `json:"started_at"`
	EndedAt    time.Time     `json:"ended_at"`
	Duration   time.Duration `json:"duration"`
	ReadingIDs []string      `json:"reading_ids"`
}
