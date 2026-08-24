package domain

import "time"

type ProcessEscalation struct {
	ItemID            string    `json:"item_id"`
	Metrics           []string  `json:"metrics"`
	TriggerReadingIDs []string  `json:"trigger_reading_ids"`
	Reason            string    `json:"reason"`
	SuggestedRetestAt time.Time `json:"suggested_retest_at"`
	Confirmed         bool      `json:"confirmed"`
	CorrectionNote    string    `json:"correction_note,omitempty"`
}
