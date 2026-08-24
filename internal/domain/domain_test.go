package domain

import (
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	r := NewMemoryRepo()
	now := time.Now()
	i, _ := NewIncident("x", "A", "S", "高", now, []EnvironmentalReading{{ID: "r", Metric: "温度", Unit: "℃", Value: 35}}, RiskHigh, []string{"超标"}, time.Hour)
	if r.Save(i, 0) != nil {
		t.Fatal()
	}
	p := MitigationPlan{Items: []MitigationItem{{ID: "i", Description: "降温"}}}
	if i.Assign(1, "u", now.Add(time.Hour), p, "a", "q"); r.Save(i, 1) != nil {
		t.Fatal()
	}
	if i.RecordItem(2, "i", "ok", "22", "ev", "u", "q2"); r.Save(i, 2) != nil {
		t.Fatal()
	}
	if i.SubmitReview(3, "u", "q3"); r.Save(i, 3) != nil {
		t.Fatal()
	}
	if i.Verify(4, "r", "合格", "ok", "q4"); i.Status != StatusClosed {
		t.Fatal(i.Status)
	}
}
