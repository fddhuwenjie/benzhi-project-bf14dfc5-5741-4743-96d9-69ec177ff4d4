package manual_review_replay_test

import (
	"museum-preservation/internal/domain"
	"museum-preservation/internal/workflow"
	"testing"
	"time"
)

func TestRejectedManualReviewRetryReturnsSameError(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repo := domain.NewMemoryRepo()
	in, err := domain.NewIncident("manual-review", "库房A", "纸本文物", "高", now,
		[]domain.EnvironmentalReading{{ID: "reading-1", Phase: domain.PhaseAbnormal, Metric: "湿度", Value: 70, Unit: "%RH", MeasuredAt: now}},
		domain.RiskHigh, []string{"湿度超标"}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	in.PendingManualReview = true
	in.ManualReviewMissing = []string{"校准状态"}
	if err = repo.Save(in, 0); err != nil {
		t.Fatal(err)
	}
	svc := &workflow.Service{Repo: repo, Now: func() time.Time { return now }}

	if _, err = svc.ConfirmManualReview(in.ID, in.Revision, false, "复核人", "reject-request"); err == nil {
		t.Fatal("首次驳回应返回禁止分派错误")
	}
	if _, err = svc.ConfirmManualReview(in.ID, in.Revision, false, "复核人", "reject-request"); err == nil {
		t.Fatal("相同驳回请求重试不得从失败翻转为成功")
	}
}
