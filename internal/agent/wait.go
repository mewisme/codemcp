package agent

import (
	"context"
	"errors"
	"time"
)

const MaxWaitDuration = 10 * time.Second

func (manager *Manager) Wait(ctx context.Context, controller Controller, id ID, afterRevision uint64, timeout time.Duration) (Snapshot, error) {
	if manager == nil {
		return Snapshot{}, errors.New("managed agent manager is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := controller.Validate(); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateID(id); err != nil {
		return Snapshot{}, err
	}
	if timeout < 0 {
		return Snapshot{}, errors.New("wait timeout cannot be negative")
	}
	if timeout > MaxWaitDuration {
		timeout = MaxWaitDuration
	}
	if timeout == 0 {
		return manager.Get(ctx, controller, id)
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var poll <-chan time.Time
	var ticker *time.Ticker
	if manager.pollInterval > 0 {
		ticker = time.NewTicker(manager.pollInterval)
		defer ticker.Stop()
		poll = ticker.C
	}

	for {
		manager.SweepExpired(ctx)
		_ = manager.refresh(ctx, controller, id)

		manager.mu.RLock()
		item, err := manager.authorizedEntryLocked(controller, id)
		if err != nil {
			manager.mu.RUnlock()
			return Snapshot{}, err
		}
		snapshot := item.record.Snapshot()
		notify := item.notify
		manager.mu.RUnlock()
		if snapshot.Revision > afterRevision || snapshot.State.Terminal() {
			return snapshot, nil
		}

		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-deadline.C:
			return manager.Get(context.Background(), controller, id)
		case <-notify:
		case <-poll:
		}
	}
}
