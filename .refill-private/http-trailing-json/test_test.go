package http_trailing_json_test

import (
	"bytes"
	"encoding/json"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/httpapi"
	"museum-preservation/internal/workflow"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateRejectsTrailingJSONWithoutMutation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := domain.NewMemoryRepo()
	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return now }}
	api := httpapi.New(svc, repo)
	body, err := json.Marshal(map[string]interface{}{
		"id": "trailing-json", "area_id": "库房A", "affected_scope": "纸本文物", "sensitivity": "高",
		"actor": "保管员", "request_id": "create-request", "observed_at": now.Format(time.RFC3339),
		"readings": []domain.EnvironmentalReading{{
			ID: "reading-1", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH",
			MeasuredAt: now, SourceNote: "现场监测", EvidenceRef: "evidence-1", EvidenceRecordedAt: now,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte(` {"unexpected":"second JSON value"}`)...)
	req := httptest.NewRequest(http.MethodPost, "/api/incidents", bytes.NewReader(body))
	res := httptest.NewRecorder()
	api.Mux.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("尾随 JSON 应返回 400，实际状态为 %d，响应为 %s", res.Code, res.Body.String())
	}
	if _, err = repo.Get("trailing-json"); err == nil {
		t.Fatal("格式错误的请求不得创建事件")
	}
}
