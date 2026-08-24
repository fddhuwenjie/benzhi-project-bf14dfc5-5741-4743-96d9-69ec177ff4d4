package domain

import "time"

type ReviewLock struct {
	Checksum    string              `json:"comparison_checksum"`
	Revision    int                 `json:"revision"`
	Comparisons []ReadingComparison `json:"comparisons"`
	ReadingIDs  []string            `json:"reading_ids"`
	LockedAt    time.Time           `json:"locked_at"`
}
