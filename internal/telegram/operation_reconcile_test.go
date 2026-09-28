package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/capability"
	runtimecontrol "go.mewis.me/codemcp/internal/runtime/control"
)

func TestDurableRestartMessageSurvivesRuntimeReplacementAndCompletesInPlace(t *testing.T) {
	root := t.TempDir()
	oldRuntime := &Runtime{root: root, generation: 1}
	oldUI, err := NewInterface(InterfaceOptions{
		Runtime: oldRuntime,
		RuntimeStatus: func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
			return runtimecontrol.RuntimeStatus{
				RunID: "run_old", ServiceID: "cm-user-test", ServiceScope: "user",
			}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := ViewOwner{ChatID: 42, UserID: 42, Generation: 1}
	state := ActionState{Route: RouteOperation, Back: RouteSystem, Operation: capability.RuntimeRestart, Confirmed: true}
	if err := oldUI.prepareDurableOperation(t.Context(), owner, 99, state); err != nil {
		t.Fatal(err)
	}

	api := &interactiveTestAPI{}
	newRuntime := &Runtime{
		root: root, api: api, generation: 2,
		health: Health{Running: true, Enabled: true, AuthorizationConfigured: true},
	}
	newUI, err := NewInterface(InterfaceOptions{
		Runtime: newRuntime,
		RuntimeStatus: func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
			return runtimecontrol.RuntimeStatus{
				PID: 222, RunID: "run_new", ServiceID: "cm-user-test", ServiceScope: "user",
			}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	newUI.ReconcilePendingRuntimeOperations(t.Context())

	if len(api.edited) != 1 {
		t.Fatalf("terminal restart edits=%d want=1", len(api.edited))
	}
	terminal := api.edited[0]
	if !strings.Contains(terminal.Text, "Restart completed") && (terminal.Rich == nil || !strings.Contains(RichFallback(terminal.Rich).Text, "Restart completed")) {
		t.Fatalf("restart terminal screen=%#v", terminal)
	}
	if records := newUI.operations.list(); len(records) != 0 {
		t.Fatalf("durable restart record was not cleared: %#v", records)
	}
}

func TestDurableRestartMessageDoesNotCompleteForSameOrWrongRuntime(t *testing.T) {
	record := durableOperationMessage{
		ChatID: 42, MessageID: 99, Operation: string(capability.RuntimeRestart),
		PreviousRunID: "run_old", ServiceID: "cm-user-test", ServiceScope: "user", CreatedAt: time.Now(),
	}
	for name, status := range map[string]runtimecontrol.RuntimeStatus{
		"same session":    {RunID: "run_old", ServiceID: "cm-user-test", ServiceScope: "user"},
		"wrong service":   {RunID: "run_new", ServiceID: "cm-other", ServiceScope: "user"},
		"wrong scope":     {RunID: "run_new", ServiceID: "cm-user-test", ServiceScope: "system"},
		"still starting":  {RunID: "run_new", ServiceID: "cm-user-test", ServiceScope: "user", Starting: true},
		"missing session": {ServiceID: "cm-user-test", ServiceScope: "user"},
	} {
		t.Run(name, func(t *testing.T) {
			if replacementRuntimeMatches(record, status) {
				t.Fatalf("unexpected replacement match: %#v", status)
			}
		})
	}
	if !replacementRuntimeMatches(record, runtimecontrol.RuntimeStatus{RunID: "run_new", ServiceID: "cm-user-test", ServiceScope: "user"}) {
		t.Fatal("valid replacement runtime was rejected")
	}
}

func TestPrepareDurableRestartRequiresCurrentRuntimeIdentity(t *testing.T) {
	root := t.TempDir()
	runtime := &Runtime{root: root, generation: 1}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime,
		RuntimeStatus: func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
			return runtimecontrol.RuntimeStatus{}, false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ui.prepareDurableOperation(t.Context(), ViewOwner{ChatID: 42, UserID: 42, Generation: 1}, 9, ActionState{Operation: capability.RuntimeRestart})
	if err == nil || !strings.Contains(err.Error(), "running runtime session") {
		t.Fatalf("prepare restart handoff error=%v", err)
	}
	if records := ui.operations.list(); len(records) != 0 {
		t.Fatalf("invalid handoff persisted records: %#v", records)
	}
}

func TestFailedRestartClearsDurableMessageRecord(t *testing.T) {
	root := t.TempDir()
	runtime := &Runtime{root: root, generation: 1}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime,
		RuntimeStatus: func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
			return runtimecontrol.RuntimeStatus{RunID: "run_old"}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := ActionState{Operation: capability.RuntimeRestart}
	if err := ui.prepareDurableOperation(t.Context(), ViewOwner{ChatID: 42, UserID: 42, Generation: 1}, 9, state); err != nil {
		t.Fatal(err)
	}
	ui.clearDurableOperation(42, 9, state)
	if records := ui.operations.list(); len(records) != 0 {
		t.Fatalf("failed restart retained durable handoff: %#v", records)
	}
	reloaded := newOperationMessageStore(root)
	if records := reloaded.list(); len(records) != 0 {
		t.Fatalf("failed restart persisted after reload: %#v", records)
	}
}

func TestRestartHandoffSurvivesSelfShutdownCancellation(t *testing.T) {
	state := ActionState{Operation: capability.RuntimeRestart}
	if !preserveDurableOperationOnError(state, context.Canceled) {
		t.Fatal("self-restart cancellation would clear durable handoff")
	}
	if !preserveDurableOperationOnError(state, context.DeadlineExceeded) {
		t.Fatal("restart handoff timeout would clear durable handoff")
	}
	if preserveDurableOperationOnError(state, errors.New("permission denied")) {
		t.Fatal("ordinary restart failure would incorrectly retain durable handoff")
	}
	if preserveDurableOperationOnError(ActionState{Operation: capability.RuntimeDown}, context.Canceled) {
		t.Fatal("non-restart operation incorrectly uses durable restart handoff")
	}
}

func TestExpiredDurableRestartRecordIsDiscarded(t *testing.T) {
	root := t.TempDir()
	api := &interactiveTestAPI{}
	runtime := &Runtime{root: root, api: api, generation: 2, health: Health{Running: true}}
	ui, err := NewInterface(InterfaceOptions{
		Runtime: runtime,
		RuntimeStatus: func(context.Context) (runtimecontrol.RuntimeStatus, bool, error) {
			return runtimecontrol.RuntimeStatus{}, false, errors.New("should not be called")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ui.operations.put(durableOperationMessage{
		ChatID: 42, MessageID: 9, Operation: string(capability.RuntimeRestart),
		PreviousRunID: "run_old", CreatedAt: time.Now().Add(-durableOperationTTL - time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	ui.ReconcilePendingRuntimeOperations(t.Context())
	if len(api.edited) != 0 {
		t.Fatalf("expired handoff edited Telegram message: %#v", api.edited)
	}
	if records := ui.operations.list(); len(records) != 0 {
		t.Fatalf("expired handoff not discarded: %#v", records)
	}
}
