package domain

import "time"

type Verification struct {
	ID                  string               `json:"id"`
	IncidentID          string               `json:"incident_id"`
	Reviewer            string               `json:"reviewer"`
	Decision            string               `json:"decision"`
	Reason              string               `json:"reason,omitempty"`
	ReturnCategory      string               `json:"return_category,omitempty"`
	ComparedReadingIDs  []string             `json:"compared_reading_ids"`
	Comparisons         []ReadingComparison  `json:"comparisons"`
	VerifiedAt          time.Time            `json:"verified_at"`
	Round               int                  `json:"round"`
	ConfirmedReadingIDs []string             `json:"confirmed_reading_ids"`
	ResponsibilityCheck ResponsibilityCheck  `json:"responsibility_check"`
	MetricDecisions     []MetricVerification `json:"metric_decisions,omitempty"`
}
