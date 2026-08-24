package domain

import "time"

type EnvironmentalReading struct {
	ID                 string       `json:"id"`
	IncidentID         string       `json:"incident_id,omitempty"`
	Phase              ReadingPhase `json:"phase"`
	Metric             string       `json:"metric"`
	Value              float64      `json:"value"`
	Unit               string       `json:"unit"`
	OriginalValue      float64      `json:"original_value"`
	OriginalUnit       string       `json:"original_unit"`
	MeasuredAt         time.Time    `json:"measured_at"`
	SourceNote         string       `json:"source_note"`
	EvidenceRef        string       `json:"evidence_ref"`
	EvidenceRecordedAt time.Time    `json:"evidence_recorded_at"`
	ReplacedByID       string       `json:"replaced_by_id,omitempty"`
	ReplacesReadingID  string       `json:"replaces_reading_id,omitempty"`
	Credibility        string       `json:"credibility,omitempty"`
	CredibilityLevel   string       `json:"credibility_level,omitempty"`
	CollectionSource   string       `json:"collection_source,omitempty"`
	Source             string       `json:"source,omitempty"`
	CalibrationStatus  string       `json:"calibration_status,omitempty"`
	CalibrationState   string       `json:"calibration_state,omitempty"`
	CalibrationAt      *time.Time   `json:"calibration_at,omitempty"`
}
