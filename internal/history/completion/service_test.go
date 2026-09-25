package completion

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestCompletionAcceptSanitizesBeforePersistenceAndEvent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	now := time.Date(2026, 9, 25, 2, 0, 0, 0, time.UTC)
	service, err := NewWorkspaceService(manager, Options{
		Now:   func() time.Time { return now },
		NewID: func() (string, error) { return "completion_test", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := service.SubscribeSnapshot(0)
	defer service.Unsubscribe(sub)

	agentID := DeriveAgentID("apc_trusted", "instance_generation")
	secret := "super-secret-token-value"
	record, created, err := service.Accept(Identity{AgentID: agentID, Source: "tunnel"}, Input{
		WorkspaceID: item.ID, Status: StatusCompleted,
		Title:   "Done token=" + secret,
		Summary: "Verified with Authorization: Bearer " + secret,
	})
	if err != nil || !created {
		t.Fatalf("record=%#v created=%t err=%v", record, created, err)
	}
	if record.Title != "Done token=<redacted>" || strings.Contains(record.Summary, secret) || !strings.Contains(record.Summary, "<redacted>") {
		t.Fatalf("unsanitized record=%#v", record)
	}
	select {
	case event := <-sub.Events:
		if event.Name != EventAccepted || event.ID != "completion-event:"+record.ID || event.Sequence != record.Sequence || strings.Contains(event.Record.Summary, secret) {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("completion event not published")
	}

	path, err := workspacestate.New(item.Path).StatePath(historyFileName)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "apc_trusted") || strings.Contains(string(data), "instance_generation") {
		t.Fatalf("completion persistence leaked raw secret/correlation: %s", data)
	}
	sequenceData, err := os.ReadFile(DefaultSequencePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sequenceData), item.ID) || strings.Contains(string(sequenceData), record.Title) || strings.Contains(string(sequenceData), record.Summary) {
		t.Fatalf("global sequence metadata contains completion payload: %s", sequenceData)
	}
}

func TestCompletionDuplicateAndSupersessionSemantics(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	ids := []string{"completion_one", "completion_two"}
	index := 0
	service, err := NewWorkspaceService(manager, Options{NewID: func() (string, error) {
		value := ids[index]
		index++
		return value, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	agentID := DeriveAgentID("caller", "generation")
	identity := Identity{AgentID: agentID, Source: "mcp"}
	input := Input{WorkspaceID: item.ID, Status: StatusBlocked, Title: "Waiting", Summary: "Need access"}
	first, created, err := service.Accept(identity, input)
	if err != nil || !created || first.Sequence != 1 {
		t.Fatalf("first=%#v created=%t err=%v", first, created, err)
	}
	duplicate, created, err := service.Accept(identity, input)
	if err != nil || created || duplicate.ID != first.ID || service.LatestSequence() != 1 {
		t.Fatalf("duplicate=%#v created=%t err=%v latest=%d", duplicate, created, err, service.LatestSequence())
	}
	second, created, err := service.Accept(identity, Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Finished", Summary: "Verified"})
	if err != nil || !created || second.Sequence != 2 || second.SupersedesID != first.ID {
		t.Fatalf("second=%#v created=%t err=%v", second, created, err)
	}
	current, ok, err := service.Current(agentID, item.ID)
	if err != nil || !ok || current.ID != second.ID {
		t.Fatalf("current=%#v ok=%t err=%v", current, ok, err)
	}
	recent, err := service.Recent(10)
	if err != nil || len(recent) != 2 || recent[0].ID != first.ID || recent[1].ID != second.ID {
		t.Fatalf("recent=%#v err=%v", recent, err)
	}
	since, err := service.Since(1, 10)
	if err != nil || since.LatestSequence != 2 || len(since.Records) != 1 || since.Records[0].ID != second.ID {
		t.Fatalf("since=%#v err=%v", since, err)
	}
	got, ok, err := service.Get(first.ID)
	if err != nil || !ok || got.ID != first.ID {
		t.Fatalf("get=%#v ok=%t err=%v", got, ok, err)
	}
}

func TestCompletionHistoryIsWorkspaceOwnedBoundedAndAggregated(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	first := registerTestWorkspace(t, manager, t.TempDir())
	second := registerTestWorkspace(t, manager, t.TempDir())
	nextID := 0
	service, err := NewWorkspaceService(manager, Options{MaxRecords: 3, NewID: func() (string, error) {
		nextID++
		return "completion_" + string(rune('a'+nextID-1)), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		workspaceID := first.ID
		if index%2 == 1 {
			workspaceID = second.ID
		}
		agentID := DeriveAgentID("caller", "generation-"+string(rune('a'+index)))
		if _, created, err := service.Accept(Identity{AgentID: agentID, Source: "mcp"}, Input{WorkspaceID: workspaceID, Status: StatusPartial, Title: "Step " + string(rune('A'+index))}); err != nil || !created {
			t.Fatalf("index=%d created=%t err=%v", index, created, err)
		}
	}
	firstHistory := readTestHistory(t, first.Path)
	secondHistory := readTestHistory(t, second.Path)
	if len(firstHistory.Records) != 3 || len(secondHistory.Records) != 2 {
		t.Fatalf("first=%#v second=%#v", firstHistory.Records, secondHistory.Records)
	}
	all, err := service.Recent(4)
	if err != nil || len(all) != 4 || all[0].Sequence != 2 || all[3].Sequence != 5 {
		t.Fatalf("aggregate=%#v err=%v", all, err)
	}
}

func TestCompletionRestartPreservesAcceptedHistoryAndSequence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	service, err := NewWorkspaceService(manager, Options{NewID: func() (string, error) { return "completion_before_restart", nil }})
	if err != nil {
		t.Fatal(err)
	}
	agentID := DeriveAgentID("caller-a", "generation-a")
	first, created, err := service.Accept(Identity{AgentID: agentID, Source: "mcp"}, Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Done", Summary: "Verified"})
	if err != nil || !created || first.Sequence != 1 {
		t.Fatalf("first=%#v created=%t err=%v", first, created, err)
	}

	restarted, err := NewWorkspaceService(workspace.NewManager(workspace.DefaultStorePath()), Options{NewID: func() (string, error) { return "completion_after_restart", nil }})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := restarted.Get(first.ID)
	if err != nil || !ok || got != first || restarted.LatestSequence() != 1 {
		t.Fatalf("restart got=%#v ok=%t latest=%d err=%v", got, ok, restarted.LatestSequence(), err)
	}
	second, created, err := restarted.Accept(Identity{AgentID: DeriveAgentID("caller-b", "generation-b"), Source: "mcp"}, Input{WorkspaceID: item.ID, Status: StatusPartial, Title: "More work"})
	if err != nil || !created || second.Sequence != 2 {
		t.Fatalf("second=%#v created=%t err=%v", second, created, err)
	}
}

func TestCompletionRejectsUnknownWorkspaceBeforeSequenceAllocation(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	service, err := NewWorkspaceService(manager, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := service.Accept(Identity{AgentID: DeriveAgentID("caller", "generation"), Source: "mcp"}, Input{WorkspaceID: "ws_missing", Status: StatusCompleted, Title: "Done"})
	if err == nil || created {
		t.Fatalf("created=%t err=%v", created, err)
	}
	if _, err := os.Stat(DefaultSequencePath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown workspace consumed sequence metadata: %v", err)
	}
}

func TestDeriveAgentIDIsOpaqueAndGenerationScoped(t *testing.T) {
	first := DeriveAgentID("caller-secret", "generation-a")
	same := DeriveAgentID("caller-secret", "generation-a")
	second := DeriveAgentID("caller-secret", "generation-b")
	if len(first) != 16 || first != same || first == second || strings.Contains(first, "caller") {
		t.Fatalf("first=%q same=%q second=%q", first, same, second)
	}
	if DeriveAgentID("", "generation") != "" || DeriveAgentID("caller", "") != "" {
		t.Fatal("missing trusted identity component produced an agent id")
	}
}

func registerTestWorkspace(t *testing.T, manager *workspace.Manager, root string) workspace.Workspace {
	t.Helper()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func readTestHistory(t *testing.T, workspaceRoot string) workspaceHistory {
	t.Helper()
	path, err := workspacestate.New(workspaceRoot).StatePath(historyFileName)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value workspaceHistory
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCompletionHistoryPathIsWorkspaceLocal(t *testing.T) {
	root := t.TempDir()
	path, err := workspaceHistoryPath(workspacestate.New(root))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".cm", "state", historyFileName)
	if path != want {
		t.Fatalf("path=%q want=%q", path, want)
	}
}
