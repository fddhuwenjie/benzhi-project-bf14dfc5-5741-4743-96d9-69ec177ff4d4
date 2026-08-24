package domain

type BaselinePairing struct {
	Metric            string `json:"metric"`
	Unit              string `json:"unit"`
	BaselineReadingID string `json:"baseline_reading_id,omitempty"`
	AbnormalReadingID string `json:"abnormal_reading_id"`
	Status            string `json:"status"`
	ValidationBasis   string `json:"validation_basis"`
}
