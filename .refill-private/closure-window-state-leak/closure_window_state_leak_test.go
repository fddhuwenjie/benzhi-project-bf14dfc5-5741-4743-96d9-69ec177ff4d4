package closure_window_state_leak_test

import (
	"encoding/json"
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"museum-preservation/internal/httpapi"
	"museum-preservation/internal/workflow"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestClosureStatisticsIsolateDisjointWindows(t *testing.T) {
	repo := domain.NewMemoryRepo()
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jul := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for window, start := range []time.Time{jan, jul} {
		for n := 0; n < 64; n++ {
			id := fmt.Sprintf("closure-%d-%02d", window, n)
			observed := start.Add(time.Duration(n) * time.Hour)
			incident := &domain.PreservationIncident{
				ID: id, AreaID: "库房甲", AffectedScope: "纸本文物", ObservedAt: observed,
				Status: domain.StatusClosed, RiskLevel: domain.RiskHigh, Revision: 1,
				CreatedAt: observed, UpdatedAt: observed,
				Readings: []domain.EnvironmentalReading{{ID: id + "-humidity", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: observed}},
			}
			if err := repo.Save(incident, 0); err != nil {
				t.Fatalf("准备事件失败: %v", err)
			}
		}
	}

	svc := &workflow.Service{Repo: repo, Rules: assessment.DefaultRules(), Now: func() time.Time { return jul.Add(10 * 24 * time.Hour) }}
	api := httpapi.New(svc, repo)
	query := func(from, to time.Time) workflow.ClosureStats {
		t.Helper()
		values := url.Values{
			"from":                   []string{from.Format(time.RFC3339)},
			"to":                     []string{to.Format(time.RFC3339)},
			"area_id":                []string{"库房甲"},
			"metric":                 []string{"湿度"},
			"recurrence_window_days": []string{"30"},
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/trends?"+values.Encode(), nil)
		api.Mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("统计请求返回 %d: %s", response.Code, response.Body.String())
		}
		var result workflow.ClosureStats
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("解析统计响应失败: %v", err)
		}
		return result
	}

	first := query(jan, jan.Add(4*24*time.Hour))
	second := query(jul, jul.Add(4*24*time.Hour))
	if len(first.Results) != 1 || first.Results[0].EventCount != 64 {
		t.Fatalf("首个窗口统计错误: %#v", first.Results)
	}
	if len(second.Results) != 1 || second.Results[0].EventCount != 64 {
		t.Fatalf("第二个窗口混入前一次请求状态: %#v", second.Results)
	}
}
