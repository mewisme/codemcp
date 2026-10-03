package agent

import (
	"errors"
	"sort"
	"time"
)

var (
	ErrAgentNotFound = errors.New("managed agent not found")
	ErrAgentBusy     = errors.New("managed agent is busy")
	ErrAgentNotIdle  = errors.New("managed agent is not idle")
)

type entry struct {
	record          Record
	backend         Backend
	handle          Handle
	pendingTerminal State
	terminalAt      time.Time
	notify          chan struct{}
}

func newEntry(record Record, backend Backend) *entry {
	return &entry{record: record, backend: backend, notify: make(chan struct{})}
}

func (item *entry) changedLocked(now time.Time) {
	item.record.Revision++
	item.record.UpdatedAt = now.UTC()
	close(item.notify)
	item.notify = make(chan struct{})
}

func (item *entry) transitionLocked(to State, now time.Time) error {
	if item.record.State == to {
		return nil
	}
	if err := ValidateTransition(item.record.State, to); err != nil {
		return err
	}
	item.record.State = to
	item.changedLocked(now)
	return nil
}

func (manager *Manager) authorizedEntryLocked(controller Controller, id ID) (*entry, error) {
	item := manager.entries[id]
	if item == nil || !controller.CanControl(item.record.Owner) {
		return nil, ErrAgentNotFound
	}
	return item, nil
}

func (manager *Manager) activeCountLocked() int {
	count := 0
	for _, item := range manager.entries {
		if !item.record.State.Terminal() {
			count++
		}
	}
	return count
}

func (manager *Manager) backendActiveCountLocked(id BackendID) int {
	count := 0
	for _, item := range manager.entries {
		if item.record.Backend == id && !item.record.State.Terminal() {
			count++
		}
	}
	return count
}

func (manager *Manager) markTerminalLocked(item *entry, state State, result, errorText string, now time.Time) error {
	wasTerminal := item.record.State.Terminal()
	if !wasTerminal {
		if err := item.transitionLocked(state, now); err != nil {
			return err
		}
	} else if item.record.State != state {
		return ValidateTransition(item.record.State, state)
	}
	item.record.Result = BoundResult(result)
	item.record.Error = BoundErrorText(errorText)
	if !wasTerminal {
		item.terminalAt = now.UTC()
		manager.terminalOrder = append(manager.terminalOrder, item.record.ID)
	}
	item.pendingTerminal = ""
	item.handle = nil
	manager.pruneTerminalLocked(now)
	return nil
}

func (manager *Manager) pruneTerminalLocked(now time.Time) {
	if len(manager.terminalOrder) == 0 {
		return
	}
	kept := manager.terminalOrder[:0]
	for _, id := range manager.terminalOrder {
		item := manager.entries[id]
		if item == nil || !item.record.State.Terminal() {
			continue
		}
		if manager.terminalTTL > 0 && !item.terminalAt.IsZero() && now.Sub(item.terminalAt) >= manager.terminalTTL {
			delete(manager.entries, id)
			continue
		}
		kept = append(kept, id)
	}
	manager.terminalOrder = kept
	if overflow := len(manager.terminalOrder) - manager.maxTerminal; overflow > 0 {
		for _, id := range manager.terminalOrder[:overflow] {
			delete(manager.entries, id)
		}
		manager.terminalOrder = append([]ID(nil), manager.terminalOrder[overflow:]...)
	}
}

func sortSnapshots(values []Snapshot) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
}
