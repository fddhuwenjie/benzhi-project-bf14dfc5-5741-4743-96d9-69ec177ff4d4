package duplicatecandidatecachescope_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/httpapi"
	"museum-preservation/internal/workflow"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDuplicateCandidateCacheSeparatesCreateQueries(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	repo := domain.NewMemoryRepo()
	seedReading := reading("seed-temperature", "温度", 35, "℃", "evidence-temperature", now)
	seedIncident(t, repo, "existing-temperature", "库房甲", "纸本文物", seedReading, now)
	for n := 0; n < 63; n++ {
		filler := reading(fmt.Sprintf("filler-reading-%02d", n), "温度", 35, "℃", fmt.Sprintf("filler-evidence-%02d", n), now.Add(-72*time.Hour))
		seedIncident(t, repo, fmt.Sprintf("filler-%02d", n), "其他库房", "填充藏品", filler, now.Add(-72*time.Hour))
	}

	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	api := httpapi.New(svc, repo)

	duplicate := createRequest("attempt-temperature", "纸本文物", "request-temperature", seedReading, now)
	first := serveCreate(t, api, duplicate)
	if first.Code != http.StatusConflict {
		t.Fatalf("前一个真实重复登记应返回 409，实际为 %d: %s", first.Code, first.Body.String())
	}

	humidity := reading("new-humidity", "湿度", 70, "%RH", "evidence-humidity", now)
	independent := createRequest("independent-humidity", "纺织品", "request-humidity", humidity, now)
	second := serveCreate(t, api, independent)
	if second.Code != http.StatusCreated {
		t.Fatalf("同区域但指标和藏品范围均不同的登记应返回 201，实际为 %d: %s", second.Code, second.Body.String())
	}
	stored, err := repo.Get("independent-humidity")
	if err != nil || stored.Revision != 1 || stored.Readings[0].Metric != "湿度" {
		t.Fatalf("独立登记未按请求持久化: incident=%#v err=%v", stored, err)
	}
}

func seedIncident(t *testing.T, repo *domain.MemoryRepo, id, area, scope string, r domain.EnvironmentalReading, observedAt time.Time) {
	t.Helper()
	in, err := domain.NewIncident(id, area, scope, "中", observedAt, []domain.EnvironmentalReading{r}, domain.RiskHigh, []string{"测试数据"}, 4*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Save(in, 0); err != nil {
		t.Fatal(err)
	}
}

func reading(id, metric string, value float64, unit, evidence string, measuredAt time.Time) domain.EnvironmentalReading {
	return domain.EnvironmentalReading{
		ID: id, Phase: domain.PhaseAbnormal, Metric: metric, Value: value, Unit: unit,
		MeasuredAt: measuredAt, SourceNote: "现场监测仪", EvidenceRef: evidence, EvidenceRecordedAt: measuredAt,
	}
}

func createRequest(id, scope, requestID string, r domain.EnvironmentalReading, observedAt time.Time) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "area_id": "库房甲", "affected_scope": scope, "sensitivity": "中",
		"actor": "保管员", "request_id": requestID, "observed_at": observedAt.Format(time.RFC3339),
		"readings": []domain.EnvironmentalReading{r},
	}
}

func serveCreate(t *testing.T, api *httpapi.API, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/incidents", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	api.Mux.ServeHTTP(response, request)
	return response
}
