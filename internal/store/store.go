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
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
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
	batches := snapshotBatchRequests(s.MemoryRepo.BatchSnapshot())
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

// 批次结果中的完整聚合可由事件快照重建，落盘时只保留幂等判定所需的元数据。
func snapshotBatchRequests(records []domain.BatchRequestRecord) []domain.BatchRequestRecord {
	compacted := make([]domain.BatchRequestRecord, len(records))
	for n, record := range records {
		record.Results = nil
		compacted[n] = record
	}
	return compacted
}
