package completion

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/idgen"
	"go.mewis.me/codemcp/internal/sequence"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

const (
	DefaultMaxRecords = 256
	eventRecentLimit  = 64
	eventBufferSize   = 16
)

type Options struct {
	MaxRecords   int
	Now          func() time.Time
	NewID        func() (string, error)
	SequencePath string
	Hooks        *CompletionHookBus
}

type AcceptOptions struct {
	SkipHooks map[string]bool
}

type EventSubscription = sequence.Subscription[Event]
type EventSnapshot = sequence.Snapshot[Event]

type Service struct {
	mu         sync.Mutex
	workspaces *workspace.Manager
	sequence   *SequenceAllocator
	events     *sequence.Stream[Event]
	hooks      *CompletionHookBus
	maxRecords int
	now        func() time.Time
	newID      func() (string, error)
	closed     bool
}

func NewWorkspaceService(workspaces *workspace.Manager, options Options) (*Service, error) {
	if workspaces == nil {
		return nil, errors.New("workspace manager is required for completion history")
	}
	if options.MaxRecords <= 0 {
		options.MaxRecords = DefaultMaxRecords
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = func() (string, error) { return idgen.New("completion", 8) }
	}
	allocator, err := NewSequenceAllocator(options.SequencePath)
	if err != nil {
		return nil, err
	}
	service := &Service{
		workspaces: workspaces,
		sequence:   allocator,
		events: sequence.New[Event](eventRecentLimit, eventBufferSize, func(value *Event, sequence uint64) {
			value.Sequence = sequence
			value.Record.Sequence = sequence
		}),
		hooks:      options.Hooks,
		maxRecords: options.MaxRecords,
		now:        options.Now,
		newID:      options.NewID,
	}
	if err := service.compactHotHistories(); err != nil {
		return nil, err
	}
	latest, err := service.latestPersistedSequence()
	if err != nil {
		return nil, err
	}
	if err := allocator.EnsureAtLeast(latest); err != nil {
		return nil, err
	}
	service.events.EnsureSequence(allocator.Current())
	return service, nil
}

func (s *Service) Accept(identity Identity, input Input) (Record, bool, error) {
	return s.AcceptWithOptions(identity, input, AcceptOptions{})
}

func (s *Service) AcceptWithOptions(identity Identity, input Input, options AcceptOptions) (Record, bool, error) {
	if s == nil {
		return Record{}, false, errors.New("agent completion service is unavailable")
	}
	identity, normalized, err := normalizeInput(identity, input)
	if err != nil {
		return Record{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Record{}, false, errors.New("agent completion service is closed")
	}
	local, canonical, err := s.workspaceStore(normalized.WorkspaceID)
	if err != nil {
		return Record{}, false, err
	}
	normalized.WorkspaceID = canonical
	history, _, err := loadWorkspaceHistory(local)
	if err != nil {
		return Record{}, false, err
	}
	archived, err := loadWorkspaceArchive(local)
	if err != nil {
		return Record{}, false, err
	}
	previous, found := currentRecord(mergeCompletionRecords(archived, history.Records), identity.AgentID)
	if found && sameTerminalMeaning(previous, normalized) {
		return previous, false, nil
	}
	id, err := s.newID()
	if err != nil {
		return Record{}, false, err
	}
	sequenceNumber, err := s.sequence.Next()
	if err != nil {
		return Record{}, false, err
	}
	record := Record{
		ID: id, Sequence: sequenceNumber, AgentID: identity.AgentID, WorkspaceID: canonical,
		Status: normalized.Status, Title: normalized.Title, Summary: normalized.Summary,
		Source: identity.Source, CreatedAt: s.now().UTC(),
	}
	if found {
		record.SupersedesID = previous.ID
	}
	history.Records = append(history.Records, record)
	history, err = s.boundWorkspaceHistory(local, history, archived)
	if err != nil {
		return Record{}, false, err
	}
	if err := saveWorkspaceHistory(local, history); err != nil {
		return Record{}, false, err
	}

	accepted := eventFor(record)
	s.events.EnsureSequence(record.Sequence - 1)
	_ = s.events.Publish(accepted)
	if s.hooks != nil {
		_ = s.hooks.DispatchExcept(accepted, options.SkipHooks)
	}
	return record, true, nil
}

func (s *Service) Current(agentID, workspaceID string) (Record, bool, error) {
	if s == nil {
		return Record{}, false, errors.New("agent completion service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	local, _, err := s.workspaceStore(workspaceID)
	if err != nil {
		return Record{}, false, err
	}
	records, exists, err := loadWorkspaceRecords(local)
	if err != nil || !exists {
		return Record{}, false, err
	}
	record, found := currentRecord(records, strings.TrimSpace(agentID))
	return record, found, nil
}

func (s *Service) CurrentWorkspace(workspaceID string) (Record, bool, error) {
	if s == nil {
		return Record{}, false, errors.New("agent completion service is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	local, _, err := s.workspaceStore(workspaceID)
	if err != nil {
		return Record{}, false, err
	}
	records, exists, err := loadWorkspaceRecords(local)
	if err != nil || !exists || len(records) == 0 {
		return Record{}, false, err
	}
	return records[len(records)-1], true, nil
}

func (s *Service) Recent(limit int) ([]Record, error) {
	if s == nil {
		return nil, errors.New("agent completion service is unavailable")
	}
	if limit <= 0 {
		return []Record{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.allRecords()
	if err != nil {
		return nil, err
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	return cloneRecords(records), nil
}

func (s *Service) RecentWorkspace(workspaceID string, limit int) ([]Record, error) {
	if s == nil {
		return nil, errors.New("agent completion service is unavailable")
	}
	if limit <= 0 {
		return []Record{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	local, _, err := s.workspaceStore(workspaceID)
	if err != nil {
		return nil, err
	}
	records, exists, err := loadWorkspaceRecords(local)
	if err != nil || !exists {
		return []Record{}, err
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	return cloneRecords(records), nil
}

func (s *Service) Get(id string) (Record, bool, error) {
	if s == nil {
		return Record{}, false, errors.New("agent completion service is unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Record{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.workspaces.List()
	if err != nil {
		return Record{}, false, err
	}
	for _, item := range items {
		if !item.Available() {
			continue
		}
		local, err := s.workspaces.LocalState(item.ID)
		if err != nil {
			continue
		}
		records, exists, err := loadWorkspaceRecords(local)
		if err != nil {
			return Record{}, false, err
		}
		if !exists {
			continue
		}
		for index := len(records) - 1; index >= 0; index-- {
			if records[index].ID == id {
				return records[index], true, nil
			}
		}
	}
	return Record{}, false, nil
}

func (s *Service) Since(after uint64, limit int) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("agent completion service is unavailable")
	}
	if limit <= 0 {
		return Snapshot{LatestSequence: s.LatestSequence(), Records: []Record{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.allRecords()
	if err != nil {
		return Snapshot{}, err
	}
	result := make([]Record, 0, min(limit, len(records)))
	for _, record := range records {
		if record.Sequence <= after {
			continue
		}
		result = append(result, record)
		if len(result) == limit {
			break
		}
	}
	return Snapshot{LatestSequence: s.sequence.Current(), Records: cloneRecords(result)}, nil
}

func (s *Service) SinceWorkspace(after uint64, workspaceID string, limit int) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("agent completion service is unavailable")
	}
	if limit <= 0 {
		return Snapshot{LatestSequence: s.LatestSequence(), Records: []Record{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	local, _, err := s.workspaceStore(workspaceID)
	if err != nil {
		return Snapshot{}, err
	}
	records, exists, err := loadWorkspaceRecords(local)
	if err != nil {
		return Snapshot{}, err
	}
	if !exists {
		return Snapshot{LatestSequence: s.sequence.Current(), Records: []Record{}}, nil
	}
	result := make([]Record, 0, min(limit, len(records)))
	for _, record := range records {
		if record.Sequence <= after {
			continue
		}
		result = append(result, record)
		if len(result) == limit {
			break
		}
	}
	return Snapshot{LatestSequence: s.sequence.Current(), Records: cloneRecords(result)}, nil
}

func (s *Service) LatestSequence() uint64 {
	if s == nil || s.sequence == nil {
		return 0
	}
	return s.sequence.Current()
}

func (s *Service) SubscribeSnapshot(recentLimit int) (*EventSubscription, EventSnapshot) {
	if s == nil || s.events == nil {
		var stream *sequence.Stream[Event]
		return stream.Subscribe(nil, recentLimit)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.events.Subscribe(nil, recentLimit)
	}
	sub, snapshot := s.events.Subscribe(nil, recentLimit)
	s.mu.Unlock()
	return sub, snapshot
}

func (s *Service) Unsubscribe(sub *EventSubscription) {
	if s != nil && s.events != nil {
		s.events.Unsubscribe(sub)
	}
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	if s.events != nil {
		s.events.Close()
	}
}

func (s *Service) AcknowledgeOverflow(sub *EventSubscription) {
	if s != nil && s.events != nil {
		s.events.AcknowledgeOverflow(sub)
	}
}

func (s *Service) workspaceStore(workspaceID string) (workspacestate.Store, string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	canonical, err := s.workspaces.CanonicalID(workspaceID)
	if err != nil {
		return workspacestate.Store{}, "", fmt.Errorf("resolve completion workspace %s: %w", workspaceID, err)
	}
	local, err := s.workspaces.LocalState(canonical)
	if err != nil {
		return workspacestate.Store{}, "", fmt.Errorf("open completion workspace %s: %w", canonical, err)
	}
	return local, canonical, nil
}

func (s *Service) allRecords() ([]Record, error) {
	items, err := s.workspaces.List()
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0)
	for _, item := range items {
		if !item.Available() {
			continue
		}
		local, err := s.workspaces.LocalState(item.ID)
		if err != nil {
			continue
		}
		history, exists, err := loadWorkspaceRecords(local)
		if err != nil {
			return nil, err
		}
		if exists {
			records = append(records, history...)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Sequence == records[j].Sequence {
			return records[i].ID < records[j].ID
		}
		return records[i].Sequence < records[j].Sequence
	})
	return records, nil
}

func (s *Service) latestPersistedSequence() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.allRecords()
	if err != nil {
		return 0, err
	}
	var latest uint64
	for _, record := range records {
		if record.Sequence > latest {
			latest = record.Sequence
		}
	}
	return latest, nil
}

func (s *Service) compactHotHistories() error {
	items, err := s.workspaces.List()
	if err != nil {
		return err
	}
	for _, item := range items {
		if !item.Available() {
			continue
		}
		local, err := s.workspaces.LocalState(item.ID)
		if err != nil {
			continue
		}
		history, exists, err := loadWorkspaceHistory(local)
		if err != nil {
			return err
		}
		if !exists || len(history.Records) <= s.maxRecords {
			continue
		}
		archived, err := loadWorkspaceArchive(local)
		if err != nil {
			return err
		}
		history, err = s.boundWorkspaceHistory(local, history, archived)
		if err != nil {
			return err
		}
		if err := saveWorkspaceHistory(local, history); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) boundWorkspaceHistory(local workspacestate.Store, history workspaceHistory, archived []Record) (workspaceHistory, error) {
	overflow := len(history.Records) - s.maxRecords
	if overflow <= 0 {
		return history, nil
	}
	stale := append([]Record(nil), history.Records[:overflow]...)
	archivedIDs := make(map[string]struct{}, len(archived))
	for _, archivedRecord := range archived {
		archivedIDs[archivedRecord.ID] = struct{}{}
	}
	toArchive := make([]Record, 0, len(stale))
	for _, archivedRecord := range stale {
		if _, exists := archivedIDs[archivedRecord.ID]; exists {
			continue
		}
		toArchive = append(toArchive, archivedRecord)
	}
	if err := appendWorkspaceArchive(local, toArchive); err != nil {
		return workspaceHistory{}, err
	}
	history.Records = append([]Record(nil), history.Records[overflow:]...)
	return history, nil
}

func currentRecord(records []Record, agentID string) (Record, bool) {
	agentID = strings.TrimSpace(agentID)
	var selected Record
	found := false
	for _, record := range records {
		if record.AgentID != agentID {
			continue
		}
		if !found || record.Sequence > selected.Sequence {
			selected, found = record, true
		}
	}
	return selected, found
}

func cloneRecords(records []Record) []Record {
	if records == nil {
		return []Record{}
	}
	return append([]Record(nil), records...)
}

func mergeCompletionRecords(groups ...[]Record) []Record {
	byID := map[string]Record{}
	for _, records := range groups {
		for _, record := range records {
			current, exists := byID[record.ID]
			if !exists || record.Sequence >= current.Sequence {
				byID[record.ID] = record
			}
		}
	}
	result := make([]Record, 0, len(byID))
	for _, record := range byID {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Sequence == result[j].Sequence {
			return result[i].ID < result[j].ID
		}
		return result[i].Sequence < result[j].Sequence
	})
	return result
}
