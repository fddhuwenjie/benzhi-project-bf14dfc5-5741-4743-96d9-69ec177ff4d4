package domain

type ReadingComparison struct {
	Metric            string   `json:"metric"`
	Unit              string   `json:"unit"`
	BaselineReadingID string   `json:"baseline_reading_id,omitempty"`
	AbnormalReadingID string   `json:"abnormal_reading_id"`
	EffectReadingID   string   `json:"effect_reading_id,omitempty"`
	BaselineValue     *float64 `json:"baseline_value,omitempty"`
	AbnormalValue     float64  `json:"abnormal_value"`
	EffectValue       *float64 `json:"effect_value,omitempty"`
	Change            *float64 `json:"change,omitempty"`
	RecoveryPercent   *float64 `json:"recovery_percent,omitempty"`
	WithinThreshold   bool     `json:"within_threshold"`
}
