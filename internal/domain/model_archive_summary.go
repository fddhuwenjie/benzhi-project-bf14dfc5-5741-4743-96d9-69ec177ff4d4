package domain

import "time"

type ArchiveSummary struct {
	Version          string                   `json:"version"`
	Checksum         string                   `json:"checksum"`
	ChecksumStatus   string                   `json:"checksum_status"`
	IncidentID       string                   `json:"incident_id"`
	AreaID           string                   `json:"area_id"`
	AffectedScope    string                   `json:"affected_scope"`
	Sensitivity      string                   `json:"sensitivity"`
	RiskLevel        RiskLevel                `json:"risk_level"`
	RiskBasis        []string                 `json:"risk_basis"`
	ObservedAt       time.Time                `json:"observed_at"`
	CreatedAt        time.Time                `json:"created_at"`
	AssignedAt       *time.Time               `json:"assigned_at,omitempty"`
	ClosedAt         time.Time                `json:"closed_at"`
	ResponseOverdue  bool                     `json:"response_overdue"`
	TreatmentOverdue bool                     `json:"treatment_overdue"`
	OverdueNotes     []string                 `json:"overdue_notes,omitempty"`
	Rounds           []ArchiveRoundSummary    `json:"rounds"`
	FinalReadings    []EnvironmentalReading   `json:"final_readings"`
	EvidenceRefs     []string                 `json:"evidence_refs"`
	Participants     []string                 `json:"participants"`
	AffectedItems    []AffectedCollectionItem `json:"affected_items,omitempty"`
}
