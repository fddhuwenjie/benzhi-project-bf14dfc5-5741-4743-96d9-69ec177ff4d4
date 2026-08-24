package workflow

import (
	"fmt"
	"math"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Service) BackfillBaselines(id string, revision int, areaID string, readings []domain.EnvironmentalReading, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	if revision <= 0 {
		return nil, &domain.ValidationError{Field: "expected_revision", Message: "expected_revision 必须为正整数"}
	}
	digest := requestDigest(struct {
		AreaID, Actor string
		Readings      []domain.EnvironmentalReading
	}{areaID, actor, readings})
	if in, handled, err := s.reuse(requestID, "baseline-backfill", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Revision != revision {
		return nil, domain.ErrConflict
	}
	if in.Status != domain.StatusPending {
		return nil, domain.ErrState
	}
	if strings.TrimSpace(areaID) != "" && strings.TrimSpace(areaID) != in.AreaID {
		return nil, &domain.ValidationError{Field: "area_id", Message: "基线补录保存区域必须与目标事件一致"}
	}
	if strings.TrimSpace(actor) == "" {
		return nil, &domain.ValidationError{Field: "actor", Message: "补录操作人不能为空"}
	}
	if len(readings) == 0 {
		return nil, &domain.ValidationError{Field: "readings", Message: "至少需要一条 baseline 补录读数"}
	}

	now := s.now()
	ids, refs := map[string]bool{}, map[string]bool{}
	abnormalAt := map[string]time.Time{}
	active := make([]domain.EnvironmentalReading, 0, len(in.Readings)+len(readings))
	latestAbnormal := in.ObservedAt
	for _, reading := range in.Readings {
		ids[reading.ID] = true
		if ref := strings.TrimSpace(reading.EvidenceRef); ref != "" {
			refs[ref] = true
		}
		if reading.ReplacedByID != "" || reading.Phase == domain.PhaseEffect {
			continue
		}
		active = append(active, reading)
		if reading.Phase == domain.PhaseAbnormal {
			if earliest, ok := abnormalAt[reading.Metric]; !ok || reading.MeasuredAt.Before(earliest) {
				abnormalAt[reading.Metric] = reading.MeasuredAt
			}
			if reading.MeasuredAt.After(latestAbnormal) {
				latestAbnormal = reading.MeasuredAt
			}
		}
	}

	normalized := make([]domain.EnvironmentalReading, len(readings))
	for n, reading := range readings {
		field := fmt.Sprintf("readings[%d]", n)
		reading.ID = strings.TrimSpace(reading.ID)
		reading.SourceNote = strings.TrimSpace(reading.SourceNote)
		reading.EvidenceRef = strings.TrimSpace(reading.EvidenceRef)
		if reading.Phase != domain.PhaseBaseline {
			return nil, &domain.ValidationError{Field: field + ".phase", Message: "基线补录读数的 phase 必须为 baseline"}
		}
		if reading.ID == "" {
			return nil, &domain.ValidationError{Field: field + ".id", Message: "读数标识不能为空"}
		}
		if ids[reading.ID] {
			return nil, &domain.ValidationError{Field: field + ".id", Message: "读数标识已存在或在本次请求中重复"}
		}
		ids[reading.ID] = true
		if reading.SourceNote == "" || utf8.RuneCountInString(reading.SourceNote) > 500 {
			return nil, &domain.ValidationError{Field: field + ".source_note", Message: "来源说明不能为空且不得超过 500 个字符"}
		}
		if reading.EvidenceRef == "" || utf8.RuneCountInString(reading.EvidenceRef) > 500 {
			return nil, &domain.ValidationError{Field: field + ".evidence_ref", Message: "证据引用不能为空且不得超过 500 个字符"}
		}
		if refs[reading.EvidenceRef] {
			return nil, &domain.ValidationError{Field: field + ".evidence_ref", Message: "证据引用已存在或在本次请求中重复"}
		}
		refs[reading.EvidenceRef] = true
		if reading.MeasuredAt.IsZero() {
			return nil, &domain.ValidationError{Field: field + ".measured_at", Message: "基线测量时间不能为空"}
		}
		if reading.EvidenceRecordedAt.IsZero() || reading.EvidenceRecordedAt.Before(reading.MeasuredAt) || reading.EvidenceRecordedAt.After(now) {
			return nil, &domain.ValidationError{Field: field + ".evidence_recorded_at", Message: "证据时间必须介于对应测量与补录提交之间"}
		}
		if math.IsNaN(reading.Value) || math.IsInf(reading.Value, 0) {
			return nil, &domain.ValidationError{Field: field + ".value", Message: "基线读数值必须为有限数值"}
		}
		normalized[n], err = assessment.Normalize(reading)
		if err != nil {
			return nil, &domain.ValidationError{Field: field + ".unit", Message: err.Error()}
		}
		if !credibleBaselineValue(normalized[n]) {
			return nil, &domain.ValidationError{Field: field + ".value", Message: "基线读数超出指标可信范围"}
		}
		earliest, ok := abnormalAt[normalized[n].Metric]
		if !ok {
			return nil, &domain.ValidationError{Field: field + ".metric", Message: "baseline 必须与事件中的有效 abnormal 指标配对"}
		}
		if !normalized[n].MeasuredAt.Before(earliest) {
			return nil, &domain.ValidationError{Field: field + ".measured_at", Message: "baseline 测量时间必须早于同指标 abnormal 读数"}
		}
		normalized[n].IncidentID = in.ID
	}

	active = append(active, normalized...)
	rules := lockedRules(in, s.Rules)
	result, err := assessment.EvaluateAt(active, in.Sensitivity, latestAbnormal, now, rules)
	if err != nil {
		return nil, err
	}
	if len(in.SensitivityTriggers) > 0 {
		result.Basis = append(result.Basis, "最高敏感级别藏品: "+strings.Join(in.SensitivityTriggers, "、"))
	}
	if err = in.AddBaselineReadings(revision, normalized, actor, requestID, now, result.Level, result.Basis, result.Response, result.Intervals, result.Pairings, result.MissingBaselines, result.RuleHits); err != nil {
		return nil, err
	}
	in.Comparisons = assessment.Compare(in.Readings, rules)
	return s.commit(in, revision, requestID, "baseline-backfill", digest)
}

func credibleBaselineValue(reading domain.EnvironmentalReading) bool {
	switch reading.Metric {
	case "温度":
		return reading.Value >= -273.15
	case "湿度":
		return reading.Value >= 0 && reading.Value <= 100
	case "光照", "污染物":
		return reading.Value >= 0
	default:
		return false
	}
}
