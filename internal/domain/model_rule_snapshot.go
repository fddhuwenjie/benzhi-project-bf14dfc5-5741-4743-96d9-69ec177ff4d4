package domain

import "time"

type RuleSnapshot struct {
	Version         string        `json:"version,omitempty"`
	Sensitivity     string        `json:"sensitivity,omitempty"`
	TemperatureMin  float64       `json:"temperature_min"`
	TemperatureMax  float64       `json:"temperature_max"`
	HumidityMin     float64       `json:"humidity_min"`
	HumidityMax     float64       `json:"humidity_max"`
	LightMax        float64       `json:"light_max"`
	PollutantMax    float64       `json:"pollutant_max"`
	StabilityWindow time.Duration `json:"stability_window"`
}
