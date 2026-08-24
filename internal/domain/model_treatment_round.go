package domain

import "time"

type TreatmentRound struct {
	Number         int                 `json:"number"`
	Plan           MitigationPlan      `json:"plan"`
	Verification   *Verification       `json:"verification,omitempty"`
	StartedAt      time.Time           `json:"started_at"`
	FrozenAt       *time.Time          `json:"frozen_at,omitempty"`
	ReturnedReason string              `json:"returned_reason,omitempty"`
	Comparisons    []ReadingComparison `json:"comparisons,omitempty"`
}
