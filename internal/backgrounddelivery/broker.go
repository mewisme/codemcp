package backgrounddelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"go.mewis.me/codemcp/internal/idgen"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

const (
	defaultRetention       = time.Hour
	defaultMaxDeliveries   = 512
	defaultMaxPerOwner     = 64
	defaultRecentTerminals = 256
	defaultRecentTTL       = 10 * time.Minute
)

var ErrUnauthorized = errors.New("background delivery scope is unauthorized")

type Owner struct {
	ID         string `json:"id"`
	Generation string `json:"generation"`
}

func (o Owner) Valid() bool {
	return strings.TrimSpace(o.ID) != "" && strings.TrimSpace(o.Generation) != ""
}

func DeriveOwner(subject, organization, generation string) Owner {
	subject = strings.TrimSpace(subject)
	organization = strings.TrimSpace(organization)
	generation = strings.TrimSpace(generation)
	if subject == "" || generation == "" {
		return Owner{}
	}
	return Owner{
		ID:         digest("owner", organization, subject),
		Generation: digest("generation", generation),
	}
}

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:24]
}

type ownerContextKey struct{}

func WithOwner(ctx context.Context, owner Owner) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	owner.ID = strings.TrimSpace(owner.ID)
	owner.Generation = strings.TrimSpace(owner.Generation)
	return context.WithValue(ctx, ownerContextKey{}, owner)
}

func OwnerFromContext(ctx context.Context) Owner {
	if ctx == nil {
		return Owner{}
	}
	owner, _ := ctx.Value(ownerContextKey{}).(Owner)
	owner.ID = strings.TrimSpace(owner.ID)
	owner.Generation = strings.TrimSpace(owner.Generation)
	return owner
}

type Registration struct {
	WorkspaceID string
	ProcessID   string
	ExecutionID string
	TaskID      string
	CallID      string
	Owner       Owner
}

type Delivery struct {
	ID              string                                `json:"id"`
	WorkspaceID     string                                `json:"workspace_id"`
	OwnerID         string                                `json:"owner_id"`
	OwnerGeneration string                                `json:"owner_generation"`
	ProcessID       string                                `json:"process_id"`
	ExecutionID     string                                `json:"execution_id,omitempty"`
	TaskID          string                                `json:"task_id,omitempty"`
	CallID          string                                `json:"call_id,omitempty"`
	Status          string                                `json:"status"`
	Reason          shellruntime.BackgroundTerminalReason `json:"reason"`
	ExitCode        *int                                  `json:"exit_code,omitempty"`
	Signal          *string                               `json:"signal,omitempty"`
	TimedOut        bool                                  `json:"timed_out,omitempty"`
	StartedAt       string                                `json:"started_at"`
	FinishedAt      string                                `json:"finished_at"`
	CreatedAt       time.Time                             `json:"created_at"`
	ExpiresAt       time.Time                             `json:"expires_at"`
}

type terminalRecord struct {
	event      shellruntime.BackgroundWorkTerminalEvent
	receivedAt time.Time
}

type Broker struct {
	processes     *shellruntime.ProcessManager
	sub           *shellruntime.BackgroundWorkTerminalSubscription
	mu            sync.Mutex
	registrations map[string]Registration
	deliveries    map[string]Delivery
	order         []string
	byOwner       map[string][]string
	recent        map[string]terminalRecord
	recentOrder   []string
	retention     time.Duration
	maxDeliveries int
	maxPerOwner   int
	closed        chan struct{}
	closeOnce     sync.Once
}

func New(processes *shellruntime.ProcessManager) *Broker {
	b := &Broker{
		processes:     processes,
		registrations: map[string]Registration{},
		deliveries:    map[string]Delivery{},
		byOwner:       map[string][]string{},
		recent:        map[string]terminalRecord{},
		retention:     defaultRetention,
		maxDeliveries: defaultMaxDeliveries,
		maxPerOwner:   defaultMaxPerOwner,
		closed:        make(chan struct{}),
	}
	if processes != nil {
		b.sub = processes.SubscribeTerminal()
		go b.consume()
	}
	return b
}

func (b *Broker) Close() {
	if b == nil {
		return
	}
	b.closeOnce.Do(func() {
		close(b.closed)
		if b.processes != nil && b.sub != nil {
			b.processes.UnsubscribeTerminal(b.sub)
		}
	})
}

func (b *Broker) consume() {
	if b == nil || b.sub == nil {
		return
	}
	for {
		select {
		case event, ok := <-b.sub.Events:
			if !ok {
				return
			}
			b.ApplyTerminal(event)
		case <-b.closed:
			return
		}
	}
}

func (b *Broker) RegisterStart(value Registration) bool {
	if b == nil {
		return false
	}
	value.WorkspaceID = strings.TrimSpace(value.WorkspaceID)
	value.ProcessID = strings.TrimSpace(value.ProcessID)
	value.ExecutionID = strings.TrimSpace(value.ExecutionID)
	value.TaskID = strings.TrimSpace(value.TaskID)
	value.CallID = strings.TrimSpace(value.CallID)
	value.Owner.ID = strings.TrimSpace(value.Owner.ID)
	value.Owner.Generation = strings.TrimSpace(value.Owner.Generation)
	if value.WorkspaceID == "" || value.ProcessID == "" || !value.Owner.Valid() {
		return false
	}
	now := time.Now().UTC()
	b.mu.Lock()
	b.pruneLocked(now)
	b.registrations[value.ProcessID] = value
	if recent, ok := b.recent[value.ProcessID]; ok {
		b.materializeLocked(value, recent.event, now)
		delete(b.recent, value.ProcessID)
		delete(b.registrations, value.ProcessID)
	}
	b.mu.Unlock()
	return true
}

func (b *Broker) AttachTask(processID, taskID string) bool {
	if b == nil {
		return false
	}
	processID, taskID = strings.TrimSpace(processID), strings.TrimSpace(taskID)
	b.mu.Lock()
	defer b.mu.Unlock()
	registration, ok := b.registrations[processID]
	if ok {
		registration.TaskID = taskID
		b.registrations[processID] = registration
		return true
	}
	for id, delivery := range b.deliveries {
		if delivery.ProcessID == processID {
			delivery.TaskID = taskID
			b.deliveries[id] = delivery
			return true
		}
	}
	return false
}

func (b *Broker) ApplyTerminal(event shellruntime.BackgroundWorkTerminalEvent) {
	if b == nil || strings.TrimSpace(event.ProcessID) == "" {
		return
	}
	now := time.Now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	if registration, ok := b.registrations[event.ProcessID]; ok {
		b.materializeLocked(registration, event, now)
		delete(b.registrations, event.ProcessID)
		return
	}
	b.recent[event.ProcessID] = terminalRecord{event: cloneTerminal(event), receivedAt: now}
	b.recentOrder = append(b.recentOrder, event.ProcessID)
	b.pruneRecentLocked(now)
}

func (b *Broker) materializeLocked(registration Registration, event shellruntime.BackgroundWorkTerminalEvent, now time.Time) {
	for _, id := range b.byOwner[ownerKey(registration.Owner)] {
		if existing, ok := b.deliveries[id]; ok && existing.ProcessID == event.ProcessID {
			return
		}
	}
	delivery := Delivery{
		ID:              idgen.Must("delivery", 12),
		WorkspaceID:     registration.WorkspaceID,
		OwnerID:         registration.Owner.ID,
		OwnerGeneration: registration.Owner.Generation,
		ProcessID:       event.ProcessID,
		ExecutionID:     firstNonEmpty(registration.ExecutionID, event.ExecutionID),
		TaskID:          registration.TaskID,
		CallID:          firstNonEmpty(registration.CallID, event.CallID),
		Status:          event.Status,
		Reason:          event.Reason,
		ExitCode:        cloneInt(event.ExitCode),
		Signal:          cloneString(event.Signal),
		TimedOut:        event.TimedOut,
		StartedAt:       event.StartedAt,
		FinishedAt:      event.FinishedAt,
		CreatedAt:       now,
		ExpiresAt:       now.Add(b.retention),
	}
	b.deliveries[delivery.ID] = delivery
	b.order = append(b.order, delivery.ID)
	key := ownerKey(registration.Owner)
	b.byOwner[key] = append(b.byOwner[key], delivery.ID)
	b.pruneLocked(now)
}

func (b *Broker) List(workspaceID string, owner Owner) ([]Delivery, error) {
	if b == nil || !owner.Valid() || strings.TrimSpace(workspaceID) == "" {
		return nil, ErrUnauthorized
	}
	workspaceID = strings.TrimSpace(workspaceID)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(time.Now().UTC())
	ids := b.byOwner[ownerKey(owner)]
	result := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		delivery, ok := b.deliveries[id]
		if ok && delivery.WorkspaceID == workspaceID && delivery.OwnerID == owner.ID && delivery.OwnerGeneration == owner.Generation {
			result = append(result, cloneDelivery(delivery))
		}
	}
	return result, nil
}

func (b *Broker) pruneLocked(now time.Time) {
	for len(b.order) > 0 {
		id := b.order[0]
		delivery, exists := b.deliveries[id]
		if exists && len(b.deliveries) <= b.maxDeliveries && now.Before(delivery.ExpiresAt) {
			break
		}
		b.order = b.order[1:]
		if exists {
			b.removeDeliveryLocked(delivery)
		}
	}
	for key, ids := range b.byOwner {
		for len(ids) > b.maxPerOwner {
			id := ids[0]
			ids = ids[1:]
			if _, ok := b.deliveries[id]; ok {
				delete(b.deliveries, id)
				b.removeFromOrderLocked(id)
			}
		}
		if len(ids) == 0 {
			delete(b.byOwner, key)
		} else {
			b.byOwner[key] = ids
		}
	}
	b.pruneRecentLocked(now)
}

func (b *Broker) removeDeliveryLocked(delivery Delivery) {
	delete(b.deliveries, delivery.ID)
	key := ownerKey(Owner{ID: delivery.OwnerID, Generation: delivery.OwnerGeneration})
	ids := b.byOwner[key]
	filtered := ids[:0]
	for _, id := range ids {
		if id != delivery.ID {
			filtered = append(filtered, id)
		}
	}
	if len(filtered) == 0 {
		delete(b.byOwner, key)
	} else {
		b.byOwner[key] = filtered
	}
}

func (b *Broker) removeFromOrderLocked(target string) {
	filtered := b.order[:0]
	for _, id := range b.order {
		if id != target {
			filtered = append(filtered, id)
		}
	}
	b.order = filtered
}

func (b *Broker) pruneRecentLocked(now time.Time) {
	for len(b.recentOrder) > 0 {
		processID := b.recentOrder[0]
		recent, exists := b.recent[processID]
		if exists && len(b.recent) <= defaultRecentTerminals && now.Sub(recent.receivedAt) < defaultRecentTTL {
			break
		}
		b.recentOrder = b.recentOrder[1:]
		delete(b.recent, processID)
	}
}

func ownerKey(owner Owner) string { return owner.ID + "\x00" + owner.Generation }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneTerminal(event shellruntime.BackgroundWorkTerminalEvent) shellruntime.BackgroundWorkTerminalEvent {
	event.ExitCode = cloneInt(event.ExitCode)
	event.Signal = cloneString(event.Signal)
	return event
}

func cloneDelivery(value Delivery) Delivery {
	value.ExitCode = cloneInt(value.ExitCode)
	value.Signal = cloneString(value.Signal)
	return value
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
