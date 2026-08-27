package trend_query_cache_scope_test

import (
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

func TestTrendCacheSeparatesQueryWindowsAndFilters(t *testing.T) {
	repo := domain.NewMemoryRepo()
	seedTrendIncidents(t, repo, "库房甲", time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC))
	seedTrendIncidents(t, repo, "库房乙", time.Date(2025, 7, 3, 12, 0, 0, 0, time.UTC))
	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules()}
	api := httpapi.New(svc, repo)

	first := getTrends(t, api, "/api/trends?granularity=day&area_id=%E5%BA%93%E6%88%BF%E7%94%B2&from=2025-01-01T00:00:00Z&to=2025-01-03T00:00:00Z")
	if len(first.Buckets) != 3 {
		t.Fatalf("前置趋势查询未生成 3 个日桶: %d", len(first.Buckets))
	}
	second := getTrends(t, api, "/api/trends?granularity=day&area_id=%E5%BA%93%E6%88%BF%E4%B9%99&from=2025-07-01T00:00:00Z&to=2025-07-05T00:00:00Z")
	if len(second.Buckets) != 5 || !second.Buckets[0].Start.Equal(time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("第二个筛选窗口复用了首个趋势投影: buckets=%d start=%s", len(second.Buckets), second.Buckets[0].Start.Format(time.RFC3339))
	}
}

func seedTrendIncidents(t *testing.T, repo *domain.MemoryRepo, area string, observedAt time.Time) {
	t.Helper()
	for n := 0; n < 64; n++ {
		in := &domain.PreservationIncident{
			ID: fmt.Sprintf("%s-%02d", area, n), AreaID: area, ObservedAt: observedAt,
			Status: domain.StatusPending, RiskLevel: domain.RiskLow, Revision: 1,
		}
		if err := repo.Save(in, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func getTrends(t *testing.T, api *httpapi.API, target string) workflow.TrendResult {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	api.Mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("趋势接口返回 %d: %s", recorder.Code, recorder.Body.String())
	}
	var result workflow.TrendResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
