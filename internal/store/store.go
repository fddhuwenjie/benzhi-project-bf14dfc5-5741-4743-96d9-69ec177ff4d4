package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"museum-preservation/internal/domain"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	*domain.MemoryRepo
	dir string
	mu  sync.Mutex
}

func (s *Store) AuditEvents(id string) ([]domain.IncidentEvent, error) {
	snapshotEvents, err := s.MemoryRepo.AuditEvents(id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(s.dir, "events.jsonl"))
	if err != nil {
		return nil, &domain.IntegrityError{Message: "事件日志不可读取: " + err.Error()}
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var logged []domain.IncidentEvent
	for {
		var event domain.IncidentEvent
		if err = decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, &domain.IntegrityError{Message: "事件日志格式损坏: " + err.Error()}
		}
		if event.IncidentID == id {
			logged = append(logged, event)
		}
	}
	if len(logged) != len(snapshotEvents) {
		return nil, &domain.IntegrityError{Message: "事件快照与日志数量不一致"}
	}
	for n := range logged {
		if logged[n].ID != snapshotEvents[n].ID || logged[n].Sequence != n+1 || logged[n].EventType != snapshotEvents[n].EventType || logged[n].Round != snapshotEvents[n].Round {
			return nil, &domain.IntegrityError{Message: fmt.Sprintf("事件快照与日志在序号 %d 不一致", n+1)}
		}
	}
	return snapshotEvents, nil
}

type diskSnapshot struct {
	Incidents     []*domain.PreservationIncident `json:"incidents"`
	Requests      []domain.RequestRecord         `json:"requests"`
	BatchRequests []domain.BatchRequestRecord    `json:"batch_requests,omitempty"`
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Store{MemoryRepo: domain.NewMemoryRepo(), dir: dir}
	b, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// snapshot.json 缺失时，尝试从 events.jsonl 重建事件时间线与聚合提交点，
		// 使进程崩溃或磁盘故障丢失快照后仍能恢复已提交的事件日志。
		events, replayErr := readEvents(filepath.Join(dir, "events.jsonl"))
		if replayErr != nil {
			if os.IsNotExist(replayErr) {
				return s, nil
			}
			return nil, replayErr
		}
		incidents, requests := replayEvents(events)
		s.MemoryRepo.ReplaceSnapshot(incidents, requests)
		return s, nil
	}
	var state diskSnapshot
	if err = json.Unmarshal(b, &state); err != nil {
		// 兼容旧版本仅包含事件数组的快照。
		if legacyErr := json.Unmarshal(b, &state.Incidents); legacyErr != nil {
			return nil, err
		}
	}
	s.MemoryRepo.ReplaceSnapshot(state.Incidents, state.Requests)
	s.MemoryRepo.ReplaceBatchSnapshot(state.BatchRequests)
	return s, nil
}

// readEvents 读取 events.jsonl 中的全部事件记录。
func readEvents(path string) ([]domain.IncidentEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []domain.IncidentEvent
	decoder := json.NewDecoder(file)
	for {
		var event domain.IncidentEvent
		if err = decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, &domain.IntegrityError{Message: "事件日志格式损坏: " + err.Error()}
		}
		events = append(events, event)
	}
	return events, nil
}

// replayEvents 从事件日志重建聚合与幂等索引。事件日志保留了完整的
// 时间线，可恢复事件编号、修订号、状态和处置轮次；读数、计划等
// 仅存在于快照的字段无法从日志重建，留空以保证聚合可访问。
func replayEvents(events []domain.IncidentEvent) ([]*domain.PreservationIncident, []domain.RequestRecord) {
	byIncident := map[string][]domain.IncidentEvent{}
	for _, event := range events {
		byIncident[event.IncidentID] = append(byIncident[event.IncidentID], event)
	}
	incidents := make([]*domain.PreservationIncident, 0, len(byIncident))
	for id, timeline := range byIncident {
		sort.Slice(timeline, func(a, b int) bool { return timeline[a].Sequence < timeline[b].Sequence })
		last := timeline[len(timeline)-1]
		in := &domain.PreservationIncident{ID: id, Status: last.StatusAfter, Revision: last.RevisionAfter, CurrentRound: last.Round, Timeline: append([]domain.IncidentEvent(nil), timeline...)}
		if in.CurrentRound < 0 {
			in.CurrentRound = 0
		}
		if len(timeline) > 0 {
			in.CreatedAt = timeline[0].OccurredAt
			in.UpdatedAt = last.OccurredAt
		}
		if in.UpdatedAt.IsZero() {
			in.UpdatedAt = time.Now().UTC()
		}
		incidents = append(incidents, in)
	}
	sort.Slice(incidents, func(a, b int) bool { return incidents[a].ID < incidents[b].ID })
	// 幂等索引中的 RequestID 可从事件日志提取，但 Digest 与 Operation 仅存在于快照，
	// 无法恢复完整幂等记录；记录已知 RequestID 以保留索引可见性。
	seen := map[string]bool{}
	var requests []domain.RequestRecord
	for _, event := range events {
		if event.RequestID == "" || seen[event.RequestID] {
			continue
		}
		seen[event.RequestID] = true
		requests = append(requests, domain.RequestRecord{RequestID: event.RequestID, IncidentID: event.IncidentID, SuccessRevision: event.RevisionAfter})
	}
	sort.Slice(requests, func(a, b int) bool { return requests[a].RequestID < requests[b].RequestID })
	return incidents, requests
}

func (s *Store) CommitBatch(incidents []*domain.PreservationIncident, expected map[string]int, rec domain.BatchRequestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldIncidents, oldRequests := s.MemoryRepo.Snapshot()
	oldBatches := s.MemoryRepo.BatchSnapshot()
	if err := s.MemoryRepo.CommitBatch(incidents, expected, rec); err != nil {
		return err
	}
	currentIncidents, currentRequests := s.MemoryRepo.Snapshot()
	if err := s.persist(currentIncidents, currentRequests); err != nil {
		s.MemoryRepo.ReplaceSnapshot(oldIncidents, oldRequests)
		s.MemoryRepo.ReplaceBatchSnapshot(oldBatches)
		return err
	}
	return nil
}

func (s *Store) FindBatchRequest(requestID string) (domain.BatchRequestRecord, bool) {
	return s.MemoryRepo.FindBatchRequest(requestID)
}

func (s *Store) Save(in *domain.PreservationIncident, expected int) error {
	return s.commit(in, expected, domain.RequestRecord{})
}

func (s *Store) Commit(in *domain.PreservationIncident, expected int, rec domain.RequestRecord) error {
	return s.commit(in, expected, rec)
}

func (s *Store) commit(in *domain.PreservationIncident, expected int, rec domain.RequestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldIncidents, oldRequests := s.MemoryRepo.Snapshot()
	if err := s.MemoryRepo.Commit(in, expected, rec); err != nil {
		return err
	}
	incidents, requests := s.MemoryRepo.Snapshot()
	if err := s.persist(incidents, requests); err != nil {
		s.MemoryRepo.ReplaceSnapshot(oldIncidents, oldRequests)
		return err
	}
	return nil
}

func (s *Store) persist(incidents []*domain.PreservationIncident, requests []domain.RequestRecord) error {
	sort.Slice(incidents, func(a, b int) bool { return incidents[a].ID < incidents[b].ID })
	sort.Slice(requests, func(a, b int) bool { return requests[a].RequestID < requests[b].RequestID })
	batches := s.MemoryRepo.BatchSnapshot()
	sort.Slice(batches, func(a, b int) bool { return batches[a].RequestID < batches[b].RequestID })
	b, err := json.MarshalIndent(diskSnapshot{Incidents: incidents, Requests: requests, BatchRequests: batches}, "", "  ")
	if err != nil {
		return err
	}
	var eventData bytes.Buffer
	encoder := json.NewEncoder(&eventData)
	for _, in := range incidents {
		for _, event := range in.Timeline {
			if err = encoder.Encode(event); err != nil {
				return err
			}
		}
	}
	eventsTmp := filepath.Join(s.dir, "events.tmp")
	if err = os.WriteFile(eventsTmp, eventData.Bytes(), 0644); err != nil {
		return err
	}
	if err = os.Rename(eventsTmp, filepath.Join(s.dir, "events.jsonl")); err != nil {
		return err
	}
	// snapshot.json 是恢复时的提交点，最后替换可保证聚合与幂等索引同步可见。
	snapshotTmp := filepath.Join(s.dir, "snapshot.tmp")
	if err = os.WriteFile(snapshotTmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(snapshotTmp, filepath.Join(s.dir, "snapshot.json"))
}
