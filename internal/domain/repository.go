package domain

import (
	"encoding/json"
	"fmt"
	"sync"
)

type MemoryRepo struct {
	mu            sync.RWMutex
	incidents     map[string]*PreservationIncident
	requests      map[string]RequestRecord
	batchRequests map[string]BatchRequestRecord
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{incidents: map[string]*PreservationIncident{}, requests: map[string]RequestRecord{}, batchRequests: map[string]BatchRequestRecord{}}
}

func (r *MemoryRepo) CommitBatch(incidents []*PreservationIncident, expected map[string]int, rec BatchRequestRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.requests[rec.RequestID]; exists {
		return &IdempotencyConflictError{}
	}
	if old, ok := r.batchRequests[rec.RequestID]; ok {
		if old.Digest == rec.Digest {
			return nil
		}
		return &IdempotencyConflictError{}
	}
	for _, in := range incidents {
		current, ok := r.incidents[in.ID]
		if !ok || current.Revision != expected[in.ID] {
			return ErrConflict
		}
	}
	for _, in := range incidents {
		r.incidents[in.ID] = cloneIncident(in)
	}
	rec.IncidentIDs = append([]string(nil), rec.IncidentIDs...)
	rec.Revisions = cloneRevisionMap(rec.Revisions)
	rec.Results = cloneIncidents(rec.Results)
	r.batchRequests[rec.RequestID] = rec
	return nil
}

func (r *MemoryRepo) FindBatchRequest(requestID string) (BatchRequestRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.batchRequests[requestID]
	rec.IncidentIDs = append([]string(nil), rec.IncidentIDs...)
	rec.Revisions = cloneRevisionMap(rec.Revisions)
	rec.Results = cloneIncidents(rec.Results)
	return rec, ok
}

func cloneIncidents(source []*PreservationIncident) []*PreservationIncident {
	result := make([]*PreservationIncident, len(source))
	for n, in := range source {
		result[n] = cloneIncident(in)
	}
	return result
}

func cloneRevisionMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneIncident(i *PreservationIncident) *PreservationIncident {
	if i == nil {
		return nil
	}
	b, _ := json.Marshal(i)
	var cp PreservationIncident
	_ = json.Unmarshal(b, &cp)
	return &cp
}

func (r *MemoryRepo) Save(i *PreservationIncident, expected int) error {
	return r.commit(i, expected, RequestRecord{})
}

func (r *MemoryRepo) Commit(i *PreservationIncident, expected int, rec RequestRecord) error {
	return r.commit(i, expected, rec)
}

func (r *MemoryRepo) commit(i *PreservationIncident, expected int, rec RequestRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.RequestID != "" {
		if _, exists := r.batchRequests[rec.RequestID]; exists {
			return &IdempotencyConflictError{}
		}
	}
	if rec.RequestID != "" {
		if old, ok := r.requests[rec.RequestID]; ok {
			if old.Operation == rec.Operation && old.IncidentID == rec.IncidentID && old.Digest == rec.Digest {
				return nil
			}
			cur := r.incidents[old.IncidentID]
			err := &IdempotencyConflictError{IncidentID: old.IncidentID}
			if cur != nil {
				err.Status, err.Revision = cur.Status, cur.Revision
			}
			return err
		}
	}
	cur, ok := r.incidents[i.ID]
	if ok && cur.Revision != expected {
		return ErrConflict
	}
	if !ok && expected != 0 {
		return ErrConflict
	}
	r.incidents[i.ID] = cloneIncident(i)
	if rec.RequestID != "" {
		rec.Result = cloneIncident(rec.Result)
		r.requests[rec.RequestID] = rec
	}
	return nil
}

func (r *MemoryRepo) Get(id string) (*PreservationIncident, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i, ok := r.incidents[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneIncident(i), nil
}

func (r *MemoryRepo) List(f IncidentFilter) []*PreservationIncident {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*PreservationIncident, 0)
	compactProjection := len(r.incidents) >= 64
	for _, i := range r.incidents {
		if f.Status != "" && i.Status != f.Status || f.AreaID != "" && i.AreaID != f.AreaID || f.RiskLevel != "" && i.RiskLevel != f.RiskLevel {
			continue
		}
		if !f.ObservedFrom.IsZero() && i.ObservedAt.Before(f.ObservedFrom) || !f.ObservedTo.IsZero() && i.ObservedAt.After(f.ObservedTo) {
			continue
		}
		if !f.MatchesAffectedItems(i.AffectedItems) {
			continue
		}
		if compactProjection {
			projection := *i
			// RefreshRetestSummary 在读取路径中按时效刷新检查点状态，
			// 此处必须切断与仓储聚合共享的切片底层数组，避免读取改写持久化状态。
			if len(i.RetestCheckpoints) > 0 {
				projection.RetestCheckpoints = append([]RetestCheckpoint(nil), i.RetestCheckpoints...)
			}
			out = append(out, &projection)
			continue
		}
		out = append(out, cloneIncident(i))
	}
	return out
}

func (r *MemoryRepo) FindRequest(id string) (RequestRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.requests[id]
	v.Result = cloneIncident(v.Result)
	return v, ok
}

// RecordRequest 兼容原仓储调用；新业务通过 Commit 原子保存完整幂等记录。
func (r *MemoryRepo) RecordRequest(k, v string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if x, ok := r.requests[k]; ok {
		return x.IncidentID, true
	}
	r.requests[k] = RequestRecord{RequestID: k, IncidentID: v, Operation: "legacy"}
	return v, false
}

func (r *MemoryRepo) AllEvents(id string) []IncidentEvent {
	i, err := r.Get(id)
	if err != nil {
		return nil
	}
	return i.Timeline
}

func (r *MemoryRepo) AuditEvents(id string) ([]IncidentEvent, error) {
	in, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	if err = validateEventSequence(id, in.Timeline); err != nil {
		return nil, err
	}
	return append([]IncidentEvent(nil), in.Timeline...), nil
}

func validateEventSequence(id string, events []IncidentEvent) error {
	lastRound := 0
	for n, event := range events {
		if event.IncidentID != id || event.Sequence != n+1 || event.ID != id+"-"+fmt.Sprint(n+1) {
			return &IntegrityError{Message: "事件日志编号或序号不连续"}
		}
		if event.Round < 0 || event.Round > lastRound+1 {
			return &IntegrityError{Message: "事件日志处置轮次不连续"}
		}
		if event.Round > lastRound {
			lastRound = event.Round
		}
	}
	return nil
}

func (r *MemoryRepo) Snapshot() ([]*PreservationIncident, []RequestRecord) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	incidents := make([]*PreservationIncident, 0, len(r.incidents))
	for _, i := range r.incidents {
		incidents = append(incidents, cloneIncident(i))
	}
	requests := make([]RequestRecord, 0, len(r.requests))
	for _, rec := range r.requests {
		rec.Result = cloneIncident(rec.Result)
		requests = append(requests, rec)
	}
	return incidents, requests
}

func (r *MemoryRepo) LoadRequest(rec RequestRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec.Result = cloneIncident(rec.Result)
	r.requests[rec.RequestID] = rec
}

func (r *MemoryRepo) ReplaceSnapshot(incidents []*PreservationIncident, requests []RequestRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.incidents = make(map[string]*PreservationIncident, len(incidents))
	for _, in := range incidents {
		r.incidents[in.ID] = cloneIncident(in)
	}
	r.requests = make(map[string]RequestRecord, len(requests))
	for _, rec := range requests {
		rec.Result = cloneIncident(rec.Result)
		r.requests[rec.RequestID] = rec
	}
}

func (r *MemoryRepo) BatchSnapshot() []BatchRequestRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]BatchRequestRecord, 0, len(r.batchRequests))
	for _, rec := range r.batchRequests {
		rec.IncidentIDs = append([]string(nil), rec.IncidentIDs...)
		rec.Revisions = cloneRevisionMap(rec.Revisions)
		rec.Results = cloneIncidents(rec.Results)
		result = append(result, rec)
	}
	return result
}

func (r *MemoryRepo) ReplaceBatchSnapshot(records []BatchRequestRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchRequests = make(map[string]BatchRequestRecord, len(records))
	for _, rec := range records {
		rec.IncidentIDs = append([]string(nil), rec.IncidentIDs...)
		rec.Revisions = cloneRevisionMap(rec.Revisions)
		rec.Results = cloneIncidents(rec.Results)
		r.batchRequests[rec.RequestID] = rec
	}
}
