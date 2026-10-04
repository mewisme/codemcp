package agent

import (
	"errors"
	"fmt"
	"strings"
)

type CompletionEvent struct {
	AgentID     ID
	WorkspaceID string
	Status      State
}

type CompletionObserver interface {
	ObserveAcceptedCompletion(CompletionEvent) error
}

func (event CompletionEvent) Validate() error {
	if err := ValidateID(event.AgentID); err != nil {
		return err
	}
	if strings.TrimSpace(event.WorkspaceID) == "" {
		return errors.New("completion workspace_id is required")
	}
	switch event.Status {
	case StateCompleted, StatePartial, StateBlocked, StateCancelled:
		return nil
	default:
		return fmt.Errorf("invalid accepted completion status %q", event.Status)
	}
}

// ObserveAcceptedCompletion must only be called after the canonical completion
// authority has accepted and persisted the child completion.
func (manager *Manager) ObserveAcceptedCompletion(event CompletionEvent) error {
	if manager == nil {
		return errors.New("managed agent manager is unavailable")
	}
	if err := event.Validate(); err != nil {
		return err
	}
	now := manager.now()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneTerminalLocked(now)
	item := manager.entries[event.AgentID]
	if err := validateAcceptedCompletionLocked(item, event); err != nil {
		return err
	}
	if item.record.State == StateCompletionPending || item.record.State.Terminal() {
		return nil
	}
	if err := item.transitionLocked(StateCompletionPending, now); err != nil {
		return err
	}
	item.pendingTerminal = event.Status
	return nil
}

// CommitAcceptedCompletion serializes durable completion acceptance with the
// managed-agent state transition so cancellation or backend refresh cannot
// race between persistence and correlation.
func (manager *Manager) CommitAcceptedCompletion(event CompletionEvent, persist func() error) error {
	if manager == nil {
		return errors.New("managed agent manager is unavailable")
	}
	if persist == nil {
		return errors.New("managed agent completion persistence callback is required")
	}
	if err := event.Validate(); err != nil {
		return err
	}
	now := manager.now()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneTerminalLocked(now)
	item := manager.entries[event.AgentID]
	if err := validateAcceptedCompletionLocked(item, event); err != nil {
		return err
	}
	alreadyCorrelated := item.record.State == StateCompletionPending || item.record.State.Terminal()
	if err := persist(); err != nil {
		return err
	}
	if alreadyCorrelated {
		return nil
	}
	if err := item.transitionLocked(StateCompletionPending, now); err != nil {
		return fmt.Errorf("durable completion accepted but managed-agent transition failed: %w", err)
	}
	item.pendingTerminal = event.Status
	return nil
}

func validateAcceptedCompletionLocked(item *entry, event CompletionEvent) error {
	if item == nil {
		return ErrAgentNotFound
	}
	if item.record.WorkspaceID != strings.TrimSpace(event.WorkspaceID) {
		return errors.New("managed agent completion workspace mismatch")
	}
	if item.record.State == StateCompletionPending {
		if item.pendingTerminal == event.Status {
			return nil
		}
		return errors.New("managed agent already has a different pending completion")
	}
	if item.record.State.Terminal() {
		if item.record.State == event.Status {
			return nil
		}
		return fmt.Errorf("managed agent is already terminal with state %q", item.record.State)
	}
	if item.record.State != StateWorking {
		return fmt.Errorf("managed agent completion requires working state, got %q", item.record.State)
	}
	return nil
}

var _ CompletionObserver = (*Manager)(nil)
