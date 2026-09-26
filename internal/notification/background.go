package notification

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

const KindBackgroundJobFinished Kind = "background.job.finished"

type BackgroundJobPolicy struct {
	Enabled   bool
	Providers map[string]bool
}

type BackgroundJobBridgeOptions struct {
	Policy func() BackgroundJobPolicy
}

type BackgroundJobBridge struct {
	processes   *shellruntime.ProcessManager
	coordinator *Coordinator
	policy      func() BackgroundJobPolicy

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	sub         *shellruntime.BackgroundWorkTerminalSubscription
	wg          sync.WaitGroup
}

func NewBackgroundJobBridge(processes *shellruntime.ProcessManager, coordinator *Coordinator, options BackgroundJobBridgeOptions) *BackgroundJobBridge {
	policy := options.Policy
	if policy == nil {
		policy = func() BackgroundJobPolicy { return BackgroundJobPolicy{} }
	}
	return &BackgroundJobBridge{processes: processes, coordinator: coordinator, policy: policy}
}

func (b *BackgroundJobBridge) Start(parent context.Context) error {
	if b == nil || b.processes == nil {
		return errors.New("background job notification bridge is unavailable")
	}
	if b.coordinator == nil {
		return errors.New("notification coordinator is unavailable")
	}
	if parent == nil {
		parent = context.Background()
	}
	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if b.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	sub := b.processes.SubscribeTerminal()
	b.cancel, b.sub = cancel, sub
	b.wg.Add(1)
	go b.run(ctx, sub)
	return nil
}

func (b *BackgroundJobBridge) Stop() {
	if b == nil {
		return
	}
	b.lifecycleMu.Lock()
	cancel, sub := b.cancel, b.sub
	b.cancel, b.sub = nil, nil
	b.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if sub != nil && b.processes != nil {
		b.processes.UnsubscribeTerminal(sub)
	}
	b.wg.Wait()
}

func (b *BackgroundJobBridge) run(ctx context.Context, sub *shellruntime.BackgroundWorkTerminalSubscription) {
	defer b.wg.Done()
	if sub == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			b.consume(ctx, event)
		case _, ok := <-sub.Overflow:
			if !ok {
				return
			}
		}
	}
}

func (b *BackgroundJobBridge) consume(ctx context.Context, event shellruntime.BackgroundWorkTerminalEvent) {
	policy := b.policy()
	if !policy.Enabled {
		return
	}
	message, ok := backgroundJobMessage(event)
	if !ok {
		return
	}
	_ = b.coordinator.Dispatch(context.WithoutCancel(ctx), message, policy.Providers)
}

func backgroundJobMessage(event shellruntime.BackgroundWorkTerminalEvent) (Message, bool) {
	processID := strings.TrimSpace(event.ProcessID)
	if processID == "" {
		return Message{}, false
	}
	title := "Background job finished"
	switch event.Status {
	case shellruntime.ExecutionStatusSuccess:
		title = "Background job completed"
	case shellruntime.ExecutionStatusFailed:
		title = "Background job failed"
	case shellruntime.ExecutionStatusTimedOut:
		title = "Background job timed out"
	case shellruntime.ExecutionStatusCancelled:
		title = "Background job cancelled"
	case shellruntime.ExecutionStatusInterrupted:
		title = "Background job interrupted"
	}
	reason := strings.TrimSpace(string(event.Reason))
	if reason == "" {
		reason = "terminal"
	}
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "finished"
	}
	timestamp, err := time.Parse(time.RFC3339Nano, event.FinishedAt)
	if err != nil {
		timestamp = time.Now().UTC()
	}
	return Message{
		ID:          "background:" + processID,
		Kind:        KindBackgroundJobFinished,
		Title:       title,
		Body:        "Background process finished with status " + status + " (" + reason + ").",
		WorkspaceID: strings.TrimSpace(event.WorkspaceID),
		TargetTool:  strings.TrimSpace(event.Tool),
		Timestamp:   timestamp.UTC(),
	}, true
}
