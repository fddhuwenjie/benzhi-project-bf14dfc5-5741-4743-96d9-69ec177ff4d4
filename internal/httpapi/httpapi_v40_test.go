package httpapi

import (
	"bytes"
	"encoding/json"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIncidentItemFilterValidation(t *testing.T) {
	svc := &workflow.Service{Repo: domain.NewMemoryRepo(), Rules: assessment.DefaultRules()}
	api := New(svc, svc.Repo)
	for _, path := range []string{
		"/api/incidents?material=塑料",
		"/api/incidents?collection_id=",
		"/api/incidents?sensitivity=极高",
		"/api/incidents?observed_from=2026-08-25T00:00:00Z&observed_to=2026-08-24T00:00:00Z",
	} {
		response := httptest.NewRecorder()
		api.Mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("非法筛选 %s 返回 %d", path, response.Code)
		}
	}
}

func TestBaselineObservationRequiresExpectedRevision(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := domain.NewMemoryRepo()
	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	reading := domain.EnvironmentalReading{ID: "abnormal", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: now.Add(-time.Minute), SourceNote: "传感器", EvidenceRef: "abnormal-evidence", EvidenceRecordedAt: now.Add(-time.Minute)}
	in, err := svc.Create(workflow.CreateCommand{ID: "http-baseline", AreaID: "库房A", AffectedScope: "纸本", Sensitivity: "高", Actor: "保管员", RequestID: "create-http-baseline", ObservedAt: reading.MeasuredAt, SubmittedAt: now, Readings: []domain.EnvironmentalReading{reading}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]interface{}{
		"revision": in.Revision, "actor": "保管员", "request_id": "http-baseline-request",
		"readings": []domain.EnvironmentalReading{{ID: "baseline", Phase: domain.PhaseBaseline, Metric: "湿度", Value: 50, Unit: "%RH", MeasuredAt: now.Add(-time.Hour), SourceNote: "历史记录", EvidenceRef: "baseline-evidence", EvidenceRecordedAt: now.Add(-30 * time.Minute)}},
	}
	payload, _ := json.Marshal(body)
	response := httptest.NewRecorder()
	New(svc, repo).Mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/incidents/http-baseline/observations", bytes.NewReader(payload)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("缺少 expected_revision 返回 %d: %s", response.Code, response.Body.String())
	}
	stored, _ := repo.Get(in.ID)
	if stored.Revision != in.Revision || len(stored.Readings) != 1 {
		t.Fatalf("协议校验失败仍写入事件: %#v", stored)
	}
}
