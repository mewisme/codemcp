package codegraph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

type CompletionHook struct {
	runtime    func() *Runtime
	workspaces *workspace.Manager
	mu         sync.Mutex
}

func NewCompletionHook(runtimeProvider func() *Runtime, workspaces *workspace.Manager) *CompletionHook {
	return &CompletionHook{runtime: runtimeProvider, workspaces: workspaces}
}

func (h *CompletionHook) Name() string { return "codegraph" }

func (h *CompletionHook) Handle(ctx context.Context, invocation agentcompletion.HookInvocation) error {
	if h == nil || h.workspaces == nil || h.runtime == nil {
		return errors.New("CodeGraph completion hook is unavailable")
	}
	event := invocation.Event
	if event.Name != agentcompletion.EventAccepted || !eligibleCompletionStatus(event.Record.Status) {
		return nil
	}
	return h.process(ctx, event.Record)
}

func (h *CompletionHook) CatchUp(ctx context.Context, service *agentcompletion.Service) error {
	if h == nil || service == nil {
		return errors.New("CodeGraph completion catch-up is unavailable")
	}
	var after uint64
	for {
		snapshot, err := service.Since(after, agentcompletion.DefaultMaxRecords)
		if err != nil {
			return err
		}
		if len(snapshot.Records) == 0 {
			return nil
		}
		for _, record := range snapshot.Records {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if eligibleCompletionStatus(record.Status) && !h.finalOutcomeExists(record) {
				_ = h.process(ctx, record)
			}
			if record.Sequence > after {
				after = record.Sequence
			}
		}
		if after >= snapshot.LatestSequence {
			return nil
		}
	}
}

func (h *CompletionHook) Outcome(workspaceID, completionID string) (CompletionSyncOutcome, bool, error) {
	if h == nil {
		return CompletionSyncOutcome{}, false, errors.New("CodeGraph completion hook is unavailable")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.outcomeLocked(workspaceID, completionID)
}

func (h *CompletionHook) process(ctx context.Context, record agentcompletion.Record) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	if outcome, found, err := h.outcomeLocked(record.WorkspaceID, record.ID); err != nil {
		return err
	} else if found && (outcome.State == CompletionSyncSucceeded || outcome.State == CompletionSyncSkipped) {
		return nil
	}

	item, projectRoot, err := h.workspaces.ResolveDirectory(record.WorkspaceID, "")
	if err != nil {
		return h.recordFailureLocked(record, CompletionReasonUnavailable, err)
	}
	local, err := h.workspaces.LocalState(item.ID)
	if err != nil {
		return h.recordFailureLocked(record, CompletionReasonUnavailable, err)
	}
	runtime := h.runtime()
	if runtime == nil {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonUnavailable, errors.New("CodeGraph runtime is unavailable"))
	}
	runtimeStatus, err := runtime.Status()
	if err != nil {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonUnavailable, err)
	}
	if !runtimeStatus.Enabled || runtimeStatus.Resolution.Source == ExecutableDisabled {
		return saveCompletionOutcome(local, h.outcome(record, item.ID, CompletionSyncSkipped, CompletionReasonDisabled))
	}
	if runtimeStatus.Resolution.Source == ExecutableUnavailable || strings.TrimSpace(runtimeStatus.Resolution.Path) == "" {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonUnavailable, errors.New("CodeGraph executable is unavailable"))
	}

	workspaceStatus := InspectWorkspace(runtimeStatus, local, item.ID, projectRoot, ".")
	if workspaceStatus.IndexState != IndexIndexed {
		return saveCompletionOutcome(local, h.outcome(record, item.ID, CompletionSyncSkipped, CompletionReasonUnindexed))
	}
	lock, err := AcquireWorkspaceMutationLock(local)
	if err != nil {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonSyncFailed, err)
	}
	defer lock.Release()

	workspaceStatus = InspectWorkspace(runtimeStatus, local, item.ID, projectRoot, ".")
	if workspaceStatus.IndexState != IndexIndexed {
		return saveCompletionOutcome(local, h.outcome(record, item.ID, CompletionSyncSkipped, CompletionReasonUnindexed))
	}
	if _, err := ReconcileProjectConfig(ctx, local, item.ID, projectRoot, "."); err != nil {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonSyncFailed, err)
	}
	if _, err := runtime.ExecuteInDir(ctx, projectRoot, SyncArgs(projectRoot), SyncTimeout, MaxOutputBytes); err != nil {
		reason := CompletionReasonSyncFailed
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			reason = CompletionReasonTimeout
		} else if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			reason = CompletionReasonCancelled
		}
		return h.recordFailureWithStoreLocked(local, record, reason, err)
	}
	if err := RecordWorkspaceLifecycle(local, item.ID, projectRoot, ".", "sync", time.Now()); err != nil {
		return h.recordFailureWithStoreLocked(local, record, CompletionReasonSyncFailed, err)
	}
	return saveCompletionOutcome(local, h.outcome(record, item.ID, CompletionSyncSucceeded, CompletionReasonNone))
}

func (h *CompletionHook) finalOutcomeExists(record agentcompletion.Record) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	outcome, found, err := h.outcomeLocked(record.WorkspaceID, record.ID)
	return err == nil && found && (outcome.State == CompletionSyncSucceeded || outcome.State == CompletionSyncSkipped)
}

func (h *CompletionHook) outcomeLocked(workspaceID, completionID string) (CompletionSyncOutcome, bool, error) {
	local, err := h.workspaces.LocalState(workspaceID)
	if err != nil {
		return CompletionSyncOutcome{}, false, err
	}
	outcomes, err := loadCompletionOutcomes(local)
	if err != nil {
		return CompletionSyncOutcome{}, false, err
	}
	for index := len(outcomes) - 1; index >= 0; index-- {
		if outcomes[index].CompletionID == completionID {
			return outcomes[index], true, nil
		}
	}
	return CompletionSyncOutcome{}, false, nil
}

func (h *CompletionHook) recordFailureLocked(record agentcompletion.Record, reason CompletionSyncReason, cause error) error {
	local, err := h.workspaces.LocalState(record.WorkspaceID)
	if err == nil {
		_ = saveCompletionOutcome(local, h.outcome(record, record.WorkspaceID, CompletionSyncFailed, reason))
	}
	return cause
}

func (h *CompletionHook) recordFailureWithStoreLocked(local workspacestate.Store, record agentcompletion.Record, reason CompletionSyncReason, cause error) error {
	_ = saveCompletionOutcome(local, h.outcome(record, record.WorkspaceID, CompletionSyncFailed, reason))
	return cause
}

func (h *CompletionHook) outcome(record agentcompletion.Record, workspaceID string, state CompletionSyncState, reason CompletionSyncReason) CompletionSyncOutcome {
	return CompletionSyncOutcome{
		CompletionID: record.ID, Sequence: record.Sequence, WorkspaceID: workspaceID, State: state, Reason: reason, UpdatedAt: time.Now().UTC(),
	}
}
