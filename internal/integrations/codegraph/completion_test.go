package codegraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCompletionHookAcceptedCompletionSyncsAtMostOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX CodeGraph fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	binary := writeCompletionCodeGraphFixture(t, true)
	cgRuntime := New(Options{Enabled: true, ConfiguredPath: binary})
	hook := NewCompletionHook(func() *Runtime { return cgRuntime }, manager)
	bus := agentcompletion.NewCompletionHookBus(agentcompletion.HookBusOptions{Timeout: time.Second})
	if err := bus.Register(hook); err != nil {
		t.Fatal(err)
	}
	defer bus.Stop()
	service, err := agentcompletion.NewWorkspaceService(manager, agentcompletion.Options{Hooks: bus})
	if err != nil {
		t.Fatal(err)
	}
	identity := agentcompletion.Identity{AgentID: agentcompletion.DeriveAgentID("caller", "generation"), Source: "mcp"}
	input := agentcompletion.Input{WorkspaceID: item.ID, Status: agentcompletion.StatusCompleted, Title: "Done"}
	first, created, err := service.Accept(identity, input)
	if err != nil || !created {
		t.Fatalf("first=%#v created=%t err=%v", first, created, err)
	}
	duplicate, created, err := service.Accept(identity, input)
	if err != nil || created || duplicate.ID != first.ID {
		t.Fatalf("duplicate=%#v created=%t err=%v", duplicate, created, err)
	}
	outcome := waitCompletionOutcome(t, hook, item.ID, first.ID, CompletionSyncSucceeded)
	if outcome.Sequence != first.Sequence {
		t.Fatalf("outcome=%#v", outcome)
	}
	data, err := os.ReadFile(filepath.Join(project, "sync-count"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(data))); got != 1 {
		t.Fatalf("sync count=%d data=%q", got, data)
	}
	persisted, ok, err := service.Get(first.ID)
	if err != nil || !ok || persisted != first {
		t.Fatalf("completion truth changed: %#v ok=%t err=%v", persisted, ok, err)
	}
}

func TestCompletionHookDisabledAndUnindexedAreDeterministicNoOps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX CodeGraph fixture")
	}
	for _, test := range []struct {
		name    string
		enabled bool
		indexed bool
		reason  CompletionSyncReason
	}{
		{name: "disabled", enabled: false, indexed: true, reason: CompletionReasonDisabled},
		{name: "unindexed", enabled: true, indexed: false, reason: CompletionReasonUnindexed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CM_CONFIG_DIR", t.TempDir())
			manager := workspace.NewManager(workspace.DefaultStorePath())
			project := t.TempDir()
			if test.indexed {
				if err := os.Mkdir(filepath.Join(project, ".codegraph"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			item, err := manager.Register(project)
			if err != nil {
				t.Fatal(err)
			}
			binary := writeCompletionCodeGraphFixture(t, true)
			cgRuntime := New(Options{Enabled: test.enabled, ConfiguredPath: binary})
			hook := NewCompletionHook(func() *Runtime { return cgRuntime }, manager)
			record := agentcompletion.Record{ID: "completion_noop", Sequence: 1, WorkspaceID: item.ID, Status: agentcompletion.StatusCompleted}
			event := agentcompletion.Event{ID: "completion-event:" + record.ID, Name: agentcompletion.EventAccepted, Record: record}
			if err := hook.Handle(context.Background(), agentcompletion.HookInvocation{Event: event}); err != nil {
				t.Fatal(err)
			}
			outcome, found, err := hook.Outcome(item.ID, record.ID)
			if err != nil || !found || outcome.State != CompletionSyncSkipped || outcome.Reason != test.reason {
				t.Fatalf("outcome=%#v found=%t err=%v", outcome, found, err)
			}
			if _, err := os.Stat(filepath.Join(project, "sync-count")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("no-op unexpectedly synced: %v", err)
			}
		})
	}
}

func TestCompletionHookFailureIsAuxiliaryAndCatchUpRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX CodeGraph fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	item, err := manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	binary := writeCompletionCodeGraphFixture(t, false)
	cgRuntime := New(Options{Enabled: true, ConfiguredPath: binary})
	hook := NewCompletionHook(func() *Runtime { return cgRuntime }, manager)
	service, err := agentcompletion.NewWorkspaceService(manager, agentcompletion.Options{})
	if err != nil {
		t.Fatal(err)
	}
	record, created, err := service.Accept(
		agentcompletion.Identity{AgentID: agentcompletion.DeriveAgentID("caller", "generation"), Source: "mcp"},
		agentcompletion.Input{WorkspaceID: item.ID, Status: agentcompletion.StatusPartial, Title: "Partial"},
	)
	if err != nil || !created {
		t.Fatalf("record=%#v created=%t err=%v", record, created, err)
	}
	event := agentcompletion.Event{ID: "completion-event:" + record.ID, Name: agentcompletion.EventAccepted, Record: record}
	if err := hook.Handle(context.Background(), agentcompletion.HookInvocation{Event: event}); err == nil {
		t.Fatal("failing CodeGraph sync returned nil")
	}
	failed, found, err := hook.Outcome(item.ID, record.ID)
	if err != nil || !found || failed.State != CompletionSyncFailed || failed.Reason != CompletionReasonSyncFailed {
		t.Fatalf("failed=%#v found=%t err=%v", failed, found, err)
	}
	persisted, ok, err := service.Get(record.ID)
	if err != nil || !ok || persisted.Status != agentcompletion.StatusPartial {
		t.Fatalf("completion truth=%#v ok=%t err=%v", persisted, ok, err)
	}

	rewriteCompletionCodeGraphFixture(t, binary, true)
	if err := hook.CatchUp(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	succeeded, found, err := hook.Outcome(item.ID, record.ID)
	if err != nil || !found || succeeded.State != CompletionSyncSucceeded {
		t.Fatalf("succeeded=%#v found=%t err=%v", succeeded, found, err)
	}
	data, err := os.ReadFile(filepath.Join(project, "sync-count"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(data))); got != 2 {
		t.Fatalf("sync attempts=%d data=%q", got, data)
	}

	restarted := NewCompletionHook(func() *Runtime { return cgRuntime }, manager)
	if err := restarted.CatchUp(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(project, "sync-count"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(data))); got != 2 {
		t.Fatalf("restart duplicated final sync: %d data=%q", got, data)
	}
}

func TestCompletionOutcomeStoreDoesNotPersistCompletionPayloadOrProcessError(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	project := t.TempDir()
	item, err := manager.Register(project)
	if err != nil {
		t.Fatal(err)
	}
	local, err := manager.LocalState(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	outcome := CompletionSyncOutcome{CompletionID: "completion_safe", Sequence: 9, WorkspaceID: item.ID, State: CompletionSyncFailed, Reason: CompletionReasonSyncFailed, UpdatedAt: time.Now().UTC()}
	if err := saveCompletionOutcome(local, outcome); err != nil {
		t.Fatal(err)
	}
	path, err := local.StatePath(completionOutcomeFile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private summary", "token=secret", "stderr", "stdout"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("outcome store leaked %q: %s", forbidden, data)
		}
	}
}

func waitCompletionOutcome(t *testing.T, hook *CompletionHook, workspaceID, completionID string, state CompletionSyncState) CompletionSyncOutcome {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		outcome, found, err := hook.Outcome(workspaceID, completionID)
		if err == nil && found && outcome.State == state {
			return outcome
		}
		time.Sleep(5 * time.Millisecond)
	}
	outcome, _, err := hook.Outcome(workspaceID, completionID)
	t.Fatalf("timed out waiting for %s: outcome=%#v err=%v", state, outcome, err)
	return CompletionSyncOutcome{}
}

func writeCompletionCodeGraphFixture(t *testing.T, success bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codegraph")
	rewriteCompletionCodeGraphFixture(t, path, success)
	return path
}

func rewriteCompletionCodeGraphFixture(t *testing.T, path string, success bool) {
	t.Helper()
	exitCode := "2"
	if success {
		exitCode = "0"
	}
	script := "#!/bin/sh\necho sync >> \"$PWD/sync-count\"\nif [ \"" + exitCode + "\" != \"0\" ]; then echo 'token=secret stderr=boom' >&2; fi\nexit " + exitCode + "\n"
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}
