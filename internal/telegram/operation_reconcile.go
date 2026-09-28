package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

const (
	durableOperationStatusTimeout = 30 * time.Second
	durableOperationPollInterval  = 250 * time.Millisecond
)

func (ui *Interface) prepareDurableOperation(ctx context.Context, owner ViewOwner, messageID int64, state ActionState) error {
	if ui == nil || ui.operations == nil || state.Operation != capability.RuntimeRestart {
		return nil
	}
	if ui.runtimeStatus == nil {
		return errors.New("runtime restart handoff status is unavailable")
	}
	statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	status, running, err := ui.runtimeStatus(statusCtx)
	if err != nil {
		return fmt.Errorf("capture runtime restart handoff: %w", err)
	}
	if !running || strings.TrimSpace(status.RunID) == "" {
		return errors.New("runtime restart handoff requires a running runtime session")
	}
	return ui.operations.put(durableOperationMessage{
		ChatID:        owner.ChatID,
		MessageID:     messageID,
		Operation:     string(state.Operation),
		PreviousRunID: status.RunID,
		ServiceID:     status.ServiceID,
		ServiceScope:  status.ServiceScope,
		CreatedAt:     time.Now().UTC(),
	})
}

func (ui *Interface) clearDurableOperation(chatID, messageID int64, state ActionState) {
	if ui == nil || ui.operations == nil || state.Operation != capability.RuntimeRestart {
		return
	}
	_ = ui.operations.delete(chatID, messageID)
}

func preserveDurableOperationOnError(state ActionState, err error) bool {
	if state.Operation != capability.RuntimeRestart || err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (ui *Interface) ReconcilePendingRuntimeOperations(ctx context.Context) {
	if ui == nil || ui.runtime == nil || ui.operations == nil || ui.runtimeStatus == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	records := ui.operations.list()
	if len(records) == 0 {
		return
	}
	for _, record := range records {
		if strings.TrimSpace(record.Operation) != string(capability.RuntimeRestart) {
			continue
		}
		if time.Since(record.CreatedAt) > durableOperationTTL {
			_ = ui.operations.delete(record.ChatID, record.MessageID)
			continue
		}
		if status, ok := ui.waitForReplacementRuntime(ctx, record); ok {
			owner := ViewOwner{ChatID: record.ChatID, UserID: record.ChatID, Generation: ui.runtime.Generation()}
			screen, err := ui.runtimeRestartCompletedScreen(owner, status)
			if err != nil {
				continue
			}
			if err := ui.runtime.EditScreen(ctx, record.ChatID, record.MessageID, screen); err == nil {
				_ = ui.operations.delete(record.ChatID, record.MessageID)
			}
		}
	}
}

func (ui *Interface) waitForReplacementRuntime(ctx context.Context, record durableOperationMessage) (runtimecontrol.RuntimeStatus, bool) {
	deadline := time.Now().Add(durableOperationStatusTimeout)
	for {
		statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, running, err := ui.runtimeStatus(statusCtx)
		cancel()
		if err == nil && running && replacementRuntimeMatches(record, status) {
			return status, true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return runtimecontrol.RuntimeStatus{}, false
		}
		timer := time.NewTimer(durableOperationPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return runtimecontrol.RuntimeStatus{}, false
		case <-timer.C:
		}
	}
}

func replacementRuntimeMatches(record durableOperationMessage, status runtimecontrol.RuntimeStatus) bool {
	if strings.TrimSpace(status.RunID) == "" || status.RunID == record.PreviousRunID || status.Starting {
		return false
	}
	if record.ServiceID != "" && status.ServiceID != record.ServiceID {
		return false
	}
	if record.ServiceScope != "" && status.ServiceScope != record.ServiceScope {
		return false
	}
	return true
}

func (ui *Interface) runtimeRestartCompletedScreen(owner ViewOwner, status runtimecontrol.RuntimeStatus) (Screen, error) {
	system, err := ui.stateButton(owner, "System", CallbackOpen, ActionState{Route: RouteSystem, Back: RouteHome})
	if err != nil {
		return Screen{}, err
	}
	home, err := ui.homeButton(owner)
	if err != nil {
		return Screen{}, err
	}
	rows := [][]string{
		{"State", "ready"},
		{"PID", fmt.Sprint(status.PID)},
	}
	if strings.TrimSpace(status.RunID) != "" {
		rows = append(rows, []string{"Session", status.RunID})
	}
	if strings.TrimSpace(status.ServiceID) != "" {
		rows = append(rows, []string{"Service", status.ServiceID})
	}
	return Screen{
		Rich: BuildRichPresentation(
			RichBlock{Kind: RichHeading, Title: "Restart completed", Text: "The replacement managed runtime is ready."},
			RichBlock{Kind: RichTable, Rows: rows},
			RichBlock{Kind: RichDetails, Title: "Handoff", Text: "This message was completed by the replacement runtime after the previous process shut down."},
		),
		Keyboard: BoundedActionGroups(ActionGroups{Primary: []Button{system}, Navigation: []Button{home}}),
	}, nil
}
