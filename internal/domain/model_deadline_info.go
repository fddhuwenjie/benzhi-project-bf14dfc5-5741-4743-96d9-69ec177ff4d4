package domain

import "time"

type DeadlineInfo struct {
	LatestResponseAt time.Time     `json:"latest_response_at"`
	Remaining        time.Duration `json:"remaining"`
	Overdue          bool          `json:"overdue"`
	OverdueDuration  time.Duration `json:"overdue_duration"`
	AvailableFrom    time.Time     `json:"available_due_from"`
	AvailableTo      time.Time     `json:"available_due_to"`
}
