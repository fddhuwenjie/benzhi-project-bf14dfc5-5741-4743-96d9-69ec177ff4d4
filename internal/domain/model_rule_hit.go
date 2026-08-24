package domain

import "time"

type RuleHit struct {
	RuleID           string        `json:"rule_id"`
	Metric           string        `json:"metric"`
	ReadingID        string        `json:"reading_id"`
	ActualValue      float64       `json:"actual_value"`
	Unit             string        `json:"unit"`
	Boundary         string        `json:"threshold_boundary"`
	Matched          bool          `json:"matched"`
	Duration         time.Duration `json:"duration"`
	Sensitivity      string        `json:"sensitivity"`
	SensitivityBonus int           `json:"sensitivity_bonus"`
}
