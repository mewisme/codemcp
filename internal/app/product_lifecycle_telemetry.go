package app

import (
	"context"
	"strings"
	"sync"

	"go.mewis.me/codemcp/internal/approval"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

type productLifecycleTelemetry struct {
	recorder  productTelemetryRuntime
	approvals *approval.EventStream
	processes *shellruntime.ProcessManager

	mu            sync.Mutex
	cancel        context.CancelFunc
	approvalSub   *approval.EventSubscription
	backgroundSub *shellruntime.BackgroundWorkTerminalSubscription
	wg            sync.WaitGroup
}

func newProductLifecycleTelemetry(recorder productTelemetryRuntime, approvals *approval.Manager, processes *shellruntime.ProcessManager) *productLifecycleTelemetry {
	if recorder == nil {
		return nil
	}
	var approvalEvents *approval.EventStream
	if approvals != nil {
		approvalEvents = approvals.Events()
	}
	return &productLifecycleTelemetry{
		recorder:  recorder,
		approvals: approvalEvents,
		processes: processes,
	}
}

func (bridge *productLifecycleTelemetry) Start(ctx context.Context) {
	if bridge == nil || bridge.recorder == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	bridge.mu.Lock()
	if bridge.cancel != nil {
		bridge.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	bridge.cancel = cancel
	if bridge.approvals != nil {
		bridge.approvalSub = bridge.approvals.Subscribe()
	}
	if bridge.processes != nil {
		bridge.backgroundSub = bridge.processes.SubscribeTerminal()
	}
	approvalSub := bridge.approvalSub
	backgroundSub := bridge.backgroundSub
	bridge.mu.Unlock()

	if approvalSub != nil {
		bridge.wg.Add(1)
		go bridge.consumeApprovals(runCtx, approvalSub)
	}
	if backgroundSub != nil {
		bridge.wg.Add(1)
		go bridge.consumeBackground(runCtx, backgroundSub)
	}
}

func (bridge *productLifecycleTelemetry) Stop() {
	if bridge == nil {
		return
	}

	bridge.mu.Lock()
	cancel := bridge.cancel
	approvalSub := bridge.approvalSub
	backgroundSub := bridge.backgroundSub
	bridge.cancel = nil
	bridge.approvalSub = nil
	bridge.backgroundSub = nil
	bridge.mu.Unlock()

	if cancel == nil {
		return
	}
	if bridge.approvals != nil && approvalSub != nil {
		bridge.approvals.Unsubscribe(approvalSub)
	}
	if bridge.processes != nil && backgroundSub != nil {
		bridge.processes.UnsubscribeTerminal(backgroundSub)
	}
	bridge.wg.Wait()
	cancel()
}

func (bridge *productLifecycleTelemetry) consumeApprovals(ctx context.Context, sub *approval.EventSubscription) {
	defer bridge.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			name, usage, ok := approvalProductUsage(event)
			if ok {
				bridge.recorder.Record(context.Background(), name, usage)
			}
		}
	}
}

func (bridge *productLifecycleTelemetry) consumeBackground(ctx context.Context, sub *shellruntime.BackgroundWorkTerminalSubscription) {
	defer bridge.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub.Events:
			if !ok {
				return
			}
			usage, ok := backgroundProductUsage(event)
			if ok {
				bridge.recorder.Record(context.Background(), producttelemetry.EventBackgroundCompleted, usage)
			}
		}
	}
}

func approvalProductUsage(event approval.Event) (producttelemetry.EventName, producttelemetry.Usage, bool) {
	if event.Subject != approval.EventSubjectRequest {
		return "", producttelemetry.Usage{}, false
	}
	usage := producttelemetry.Usage{
		Interface:    producttelemetry.InterfaceRuntime,
		OmitDuration: true,
		OmitSuccess:  true,
	}
	switch event.Name {
	case approval.EventPending:
		usage.Feature = string(approval.StatusPending)
		return producttelemetry.EventApprovalRequested, usage, true
	case approval.EventApproved:
		usage.Feature = string(approval.StatusApproved)
	case approval.EventDenied:
		usage.Feature = string(approval.StatusDenied)
	case approval.EventExpired:
		usage.Feature = string(approval.StatusExpired)
	case approval.EventCancelled:
		usage.Feature = string(approval.StatusCancelled)
	default:
		return "", producttelemetry.Usage{}, false
	}
	return producttelemetry.EventApprovalResolved, usage, true
}

func backgroundProductUsage(event shellruntime.BackgroundWorkTerminalEvent) (producttelemetry.Usage, bool) {
	status := strings.TrimSpace(event.Status)
	switch status {
	case shellruntime.ExecutionStatusSuccess,
		shellruntime.ExecutionStatusFailed,
		shellruntime.ExecutionStatusCancelled,
		shellruntime.ExecutionStatusTimedOut,
		shellruntime.ExecutionStatusInterrupted:
	default:
		return producttelemetry.Usage{}, false
	}

	reason := event.Reason
	switch reason {
	case shellruntime.BackgroundTerminalExit,
		shellruntime.BackgroundTerminalFailure,
		shellruntime.BackgroundTerminalTimeout,
		shellruntime.BackgroundTerminalSignal,
		shellruntime.BackgroundTerminalStopped,
		shellruntime.BackgroundTerminalShutdown:
	default:
		return producttelemetry.Usage{}, false
	}

	return producttelemetry.Usage{
		Interface:    producttelemetry.InterfaceRuntime,
		Feature:      "background." + status + "." + string(reason),
		OmitDuration: true,
		OmitSuccess:  true,
	}, true
}
