package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/configformat"
	agentcompletion "go.mewis.me/codemcp/internal/history/completion"
	"go.mewis.me/codemcp/internal/tools"
)

func TestCompletionReadSurfacesShareRuntimeHistory(t *testing.T) {
	root := t.TempDir()
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(previous) }()

	runtime := tools.NewRuntime()
	defer runtime.CompletionHooks.Stop()
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity := agentcompletion.Identity{
		AgentID: agentcompletion.DeriveAgentID("cli-completion-caller", "cli-completion-generation"),
		Source:  "mcp",
	}
	first := acceptCLICompletion(t, runtime, identity, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusPartial, Title: "Partial", Summary: "First state",
	})
	second := acceptCLICompletion(t, runtime, identity, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusBlocked, Title: "Blocked", Summary: "Second state",
	})

	control, err := startRuntimeControl(runtimeControlOptions{
		Completions: runtime.Completions,
		Reload:      func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		Status:      func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} },
		Shutdown:    func() {},
		ClearLogs:   func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	records, err := application.ListCompletions(ctx, workspace.ID, 20)
	if err != nil || len(records) != 2 || records[0].ID != first.ID || records[1].ID != second.ID {
		t.Fatalf("application list=%#v err=%v", records, err)
	}
	current, err := application.CurrentCompletion(ctx, workspace.ID)
	if err != nil || current.ID != second.ID {
		t.Fatalf("application current=%#v err=%v", current, err)
	}
	viewed, err := application.ViewCompletion(ctx, first.ID)
	if err != nil || viewed.ID != first.ID {
		t.Fatalf("application view=%#v err=%v", viewed, err)
	}

	subscription, snapshot, err := application.SubscribeCompletions(ctx, workspace.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if len(snapshot.Records) != 2 || snapshot.Records[1].ID != second.ID || snapshot.LatestSequence < second.Sequence {
		t.Fatalf("completion feed snapshot=%#v", snapshot)
	}
	third := acceptCLICompletion(t, runtime, identity, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusCompleted, Title: "Finished", Summary: "Final state",
	})
	eventCh := make(chan agentcompletion.Event, 1)
	errCh := make(chan error, 1)
	go func() {
		event, err := subscription.Next()
		if err != nil {
			errCh <- err
			return
		}
		eventCh <- event
	}()
	select {
	case event := <-eventCh:
		if event.Record.ID != third.ID || event.Record.Sequence != third.Sequence {
			t.Fatalf("completion feed event=%#v", event)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("completion feed did not observe runtime accepted event")
	}

	listJSON := executeRequestCommand(t, root, []string{"agent", "completion", "list", "--workspace", workspace.ID, "--limit", "20", "--json"})
	var cliRecords []agentcompletion.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(listJSON)), &cliRecords); err != nil {
		t.Fatalf("CLI list json=%q err=%v", listJSON, err)
	}
	if len(cliRecords) != 3 || cliRecords[0].ID != first.ID || cliRecords[2].ID != third.ID {
		t.Fatalf("CLI list=%#v", cliRecords)
	}

	currentJSON := executeRequestCommand(t, root, []string{"agent", "completion", "current", "--workspace", workspace.ID, "--json"})
	var cliCurrent agentcompletion.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(currentJSON)), &cliCurrent); err != nil || cliCurrent.ID != third.ID {
		t.Fatalf("CLI current=%q value=%#v err=%v", currentJSON, cliCurrent, err)
	}

	viewJSON := executeRequestCommand(t, root, []string{"agent", "completion", "view", second.ID, "--json"})
	var cliView agentcompletion.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewJSON)), &cliView); err != nil || cliView.ID != second.ID {
		t.Fatalf("CLI view=%q value=%#v err=%v", viewJSON, cliView, err)
	}

	direct, found, err := runtime.Completions.CurrentWorkspace(workspace.ID)
	if err != nil || !found || direct.ID != cliCurrent.ID {
		t.Fatalf("runtime current=%#v found=%t err=%v CLI=%#v", direct, found, err, cliCurrent)
	}
}

func TestCompletionCLIHumanOutputUsesSharedPresentation(t *testing.T) {
	root := t.TempDir()
	previous := configformat.RootPath()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configformat.SetRootPath(previous) }()

	runtime := tools.NewRuntime()
	defer runtime.CompletionHooks.Stop()
	workspace, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := acceptCLICompletion(t, runtime, agentcompletion.Identity{
		AgentID: agentcompletion.DeriveAgentID("cli-human-caller", "cli-human-generation"), Source: "mcp",
	}, agentcompletion.Input{
		WorkspaceID: workspace.ID, Status: agentcompletion.StatusCompleted, Title: "Finished work", Summary: "Verified",
	})
	control, err := startRuntimeControl(runtimeControlOptions{
		Completions: runtime.Completions,
		Reload:      func(context.Context) (runtimeReloadResult, error) { return runtimeReloadResult{PID: os.Getpid()}, nil },
		Status:      func() runtimeStatusResult { return runtimeStatusResult{PID: os.Getpid()} },
		Shutdown:    func() {},
		ClearLogs:   func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	output := executeRequestCommand(t, root, []string{"agent", "completion", "view", record.ID})
	if !strings.Contains(output, "Agent completion") || !strings.Contains(output, record.ID) || !strings.Contains(output, "Completed") || !strings.Contains(output, "Finished work") || !strings.Contains(output, "Done") {
		t.Fatalf("completion human output=%q", output)
	}
	if strings.Count(output, record.ID) != 1 || strings.Count(output, "Loaded agent completion") != 1 {
		t.Fatalf("completion human output duplicated lifecycle/entity state: %q", output)
	}
}

func acceptCLICompletion(t *testing.T, runtime *tools.Runtime, identity agentcompletion.Identity, input agentcompletion.Input) agentcompletion.Record {
	t.Helper()
	record, created, err := runtime.Completions.Accept(identity, input)
	if err != nil || !created {
		t.Fatalf("accept record=%#v created=%t err=%v", record, created, err)
	}
	return record
}
