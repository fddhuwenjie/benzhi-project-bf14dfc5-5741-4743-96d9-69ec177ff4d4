package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"museum-preservation/internal/assessment"
	"museum-preservation/internal/domain"
	"sort"
	"strings"
)

func (s *Service) SupplementAffectedItems(id string, revision int, items []domain.AffectedCollectionItem, note string, evidence []domain.EnvironmentalReading, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(struct {
		Items    []domain.AffectedCollectionItem
		Note     string
		Evidence []domain.EnvironmentalReading
		Actor    string
	}{items, note, evidence, actor})
	if in, handled, err := s.reuse(requestID, "affected-supplement", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	normalizedEvidence := make([]domain.EnvironmentalReading, len(evidence))
	for n, reading := range evidence {
		reading.Phase = domain.PhaseAbnormal
		if reading.EvidenceRecordedAt.IsZero() {
			reading.EvidenceRecordedAt = s.now()
		}
		if reading.ID == "" {
			reading.ID = fmt.Sprintf("%s-supplement-%d", id, len(in.Readings)+n+1)
		}
		for _, old := range in.Readings {
			if old.ID == reading.ID {
				return nil, &domain.ValidationError{Field: fmt.Sprintf("evidence_readings[%d].id", n), Message: "读数标识不得重复"}
			}
		}
		normalized, normErr := assessment.Normalize(reading)
		if normErr != nil {
			return nil, &domain.ValidationError{Field: fmt.Sprintf("evidence_readings[%d].unit", n), Message: normErr.Error()}
		}
		normalizedEvidence[n] = normalized
	}
	obs := domain.SupplementalObservation{Note: strings.TrimSpace(note), Readings: normalizedEvidence, ObservedAt: s.now()}
	if err = in.AddAffectedItems(revision, items, obs, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	in.Readings = append(in.Readings, normalizedEvidence...)
	for _, r := range normalizedEvidence {
		in.Evidence = append(in.Evidence, domain.EvidenceSummary{ReadingID: r.ID, Metric: r.Metric, Reference: r.EvidenceRef, SourceNote: r.SourceNote, RecordedAt: r.EvidenceRecordedAt})
	}
	active := make([]domain.EnvironmentalReading, 0, len(in.Readings))
	for _, r := range in.Readings {
		if r.ReplacedByID == "" && r.Phase != domain.PhaseEffect {
			active = append(active, r)
		}
	}
	result, evalErr := assessment.EvaluateAt(active, in.Sensitivity, in.ObservedAt, s.now(), lockedRules(in, s.Rules))
	if evalErr != nil {
		return nil, evalErr
	}
	if result.Level < in.RiskLevel {
		result.Level = in.RiskLevel
	}
	if len(in.SensitivityTriggers) > 0 {
		result.Basis = append(result.Basis, "最高敏感级别藏品: "+strings.Join(in.SensitivityTriggers, "、"))
	}
	in.RiskLevel, in.RiskBasis, in.ResponseDue = result.Level, result.Basis, result.Response
	in.RefreshDeadline(s.now())
	in.Comparisons = assessment.Compare(in.Readings, lockedRules(in, s.Rules))
	return s.commit(in, revision, requestID, "affected-supplement", digest)
}

func (s *Service) SetRetestCheckpoints(id string, revision int, checkpoints []domain.RetestCheckpoint, actor, requestID string) (*domain.PreservationIncident, error) {
	if err := requireRequestID(requestID); err != nil {
		return nil, err
	}
	digest := requestDigest(checkpoints)
	if in, handled, err := s.reuse(requestID, "retest-plan", id, digest); handled || err != nil {
		return in, err
	}
	in, err := s.Repo.Get(id)
	if err != nil {
		return nil, err
	}
	if err = in.SetRetestCheckpoints(revision, checkpoints, actor, requestID, s.now()); err != nil {
		return nil, err
	}
	return s.commit(in, revision, requestID, "retest-plan", digest)
}

func (s *Service) HandoverSnapshot(filters domain.IncidentFilter, from, to, shift, requestID string) (domain.HandoverSnapshot, error) {
	if strings.TrimSpace(requestID) == "" {
		return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "request_id", Message: "request_id 不能为空"}
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || from == to {
		return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "handover", Message: "交班人与接班人必须不同且不能为空"}
	}
	incidents := s.Repo.List(filters)
	events := make([]domain.HandoverEvent, 0)
	for _, in := range incidents {
		if in.Status == domain.StatusClosed {
			continue
		}
		for _, sig := range in.HandoverSignatures {
			if sig.Shift == shift {
				return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "shift", Message: "同一班次的事件不得重复加入交接快照"}
			}
		}
		next, block := nextAction(in)
		events = append(events, domain.HandoverEvent{IncidentID: in.ID, Revision: in.Revision, Status: in.Status, RiskLevel: in.RiskLevel, Assignee: in.Assignee, DueAt: in.DueAt, NextAction: next, BlockReason: block})
	}
	if len(events) == 0 {
		return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "events", Message: "至少需要一个未关闭事件"}
	}
	sort.Slice(events, func(a, b int) bool {
		if events[a].RiskLevel != events[b].RiskLevel {
			return riskOrder(events[a].RiskLevel) > riskOrder(events[b].RiskLevel)
		}
		return events[a].DueAt.Before(events[b].DueAt)
	})
	snap := domain.HandoverSnapshot{ID: requestID, Shift: shift, From: from, To: to, Filters: filters, CapturedAt: s.now(), Events: events}
	snap.Checksum = handoverChecksum(snap)
	return snap, nil
}

func (s *Service) SignHandover(snap domain.HandoverSnapshot, requestID, from, to string, revisions map[string]int) (domain.HandoverSnapshot, error) {
	if requestID == "" {
		return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "request_id", Message: "request_id 不能为空"}
	}
	if old, ok := s.Repo.FindRequest(requestID); ok && old.Result != nil {
		return snap, nil
	}
	allSigned := len(snap.Events) > 0
	for _, item := range snap.Events {
		in, e := s.Repo.Get(item.IncidentID)
		if e != nil {
			allSigned = false
			break
		}
		found := false
		for _, sig := range in.HandoverSignatures {
			if sig.SnapshotID == snap.ID && sig.Checksum == snap.Checksum && sig.To == to {
				found = true
				break
			}
		}
		if !found {
			allSigned = false
			break
		}
	}
	if allSigned {
		now := s.now()
		snap.SignedAt = &now
		return snap, nil
	}
	for _, item := range snap.Events {
		in, err := s.Repo.Get(item.IncidentID)
		if err != nil {
			return domain.HandoverSnapshot{}, err
		}
		if in.Revision != revisions[item.IncidentID] || in.Revision != item.Revision {
			return domain.HandoverSnapshot{}, &domain.ValidationError{Field: "revisions", Message: fmt.Sprintf("事件%s已变化，需重新生成快照", item.IncidentID)}
		}
	}
	now := s.now()
	snap.SignedAt = &now
	for _, item := range snap.Events {
		in, _ := s.Repo.Get(item.IncidentID)
		in.HandoverSignatures = append(in.HandoverSignatures, domain.HandoverSignature{SnapshotID: snap.ID, Checksum: snap.Checksum, From: from, To: to, Shift: snap.Shift, SignedAt: now})
		in.Revision++
		in.UpdatedAt = now
		in.Timeline = append(in.Timeline, domain.IncidentEvent{ID: fmt.Sprintf("%s-%d", in.ID, len(in.Timeline)+1), IncidentID: in.ID, Sequence: len(in.Timeline) + 1, EventType: "交接班签收", Actor: to, OccurredAt: now, Payload: map[string]interface{}{"snapshot_id": snap.ID, "checksum": snap.Checksum, "from": from, "to": to, "shift": snap.Shift}, RequestID: requestID, Round: in.CurrentRound, RevisionBefore: in.Revision - 1, RevisionAfter: in.Revision})
		if err := s.Repo.Commit(in, item.Revision, domain.RequestRecord{RequestID: requestID + "/" + item.IncidentID, Operation: "handover-sign", IncidentID: in.ID, Digest: snap.Checksum, SuccessRevision: in.Revision, Result: in}); err != nil {
			return domain.HandoverSnapshot{}, err
		}
	}
	s.invalidateListCache()
	return snap, nil
}

func nextAction(in *domain.PreservationIncident) (string, string) {
	if in.Status == domain.StatusPending {
		return "分派", ""
	}
	if len(in.RetestCheckpoints) > 0 {
		for _, c := range in.RetestCheckpoints {
			if c.Status != "已完成" {
				return "复测:" + c.Metric, c.Status
			}
		}
	}
	if in.Status == domain.StatusMitigating {
		return "完成措施并提交复核", ""
	}
	return "复核", ""
}
func riskOrder(r domain.RiskLevel) int {
	switch r {
	case domain.RiskCritical:
		return 4
	case domain.RiskHigh:
		return 3
	case domain.RiskMedium:
		return 2
	}
	return 1
}
func handoverChecksum(s domain.HandoverSnapshot) string {
	s.Checksum = ""
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
