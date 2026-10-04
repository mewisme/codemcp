package completion

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestCompletionHotHistoryArchivesOverflowWithoutLosingAcceptedRecords(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	nextID := 0
	service, err := NewWorkspaceService(manager, Options{MaxRecords: 2, NewID: func() (string, error) {
		nextID++
		return fmt.Sprintf("completion_%02d", nextID), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	records := make([]Record, 0, 5)
	for index := 0; index < 5; index++ {
		record, created, err := service.Accept(
			Identity{AgentID: DeriveAgentID("caller", fmt.Sprintf("generation-%d", index)), Source: "mcp"},
			Input{WorkspaceID: item.ID, Status: StatusPartial, Title: fmt.Sprintf("Step %d", index)},
		)
		if err != nil || !created {
			t.Fatalf("index=%d record=%#v created=%t err=%v", index, record, created, err)
		}
		records = append(records, record)
	}
	hot := readTestHistory(t, item.Path)
	if len(hot.Records) != 2 || hot.Records[0].ID != records[3].ID || hot.Records[1].ID != records[4].ID {
		t.Fatalf("hot=%#v", hot.Records)
	}
	local := workspacestate.New(item.Path)
	archived, err := loadWorkspaceArchive(local)
	if err != nil || len(archived) != 3 || archived[0].ID != records[0].ID || archived[2].ID != records[2].ID {
		t.Fatalf("archive=%#v err=%v", archived, err)
	}
	all, err := service.RecentWorkspace(item.ID, 10)
	if err != nil || len(all) != 5 {
		t.Fatalf("all=%#v err=%v", all, err)
	}
	first, found, err := service.Get(records[0].ID)
	if err != nil || !found || first.ID != records[0].ID {
		t.Fatalf("first=%#v found=%t err=%v", first, found, err)
	}

	restartedManager := workspace.NewManager(workspace.DefaultStorePath())
	restarted, err := NewWorkspaceService(restartedManager, Options{MaxRecords: 2, NewID: func() (string, error) { return "completion_06", nil }})
	if err != nil {
		t.Fatal(err)
	}
	restartedAll, err := restarted.RecentWorkspace(item.ID, 10)
	if err != nil || len(restartedAll) != 5 || restarted.LatestSequence() != 5 {
		t.Fatalf("restart all=%#v latest=%d err=%v", restartedAll, restarted.LatestSequence(), err)
	}
	next, created, err := restarted.Accept(
		Identity{AgentID: DeriveAgentID("caller", "generation-6"), Source: "mcp"},
		Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Final"},
	)
	if err != nil || !created || next.Sequence != 6 {
		t.Fatalf("next=%#v created=%t err=%v", next, created, err)
	}
}

func TestCompletionArchiveRetentionBoundsPersistedRecords(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	local := workspacestate.New(item.Path)
	records := make([]Record, 0, maxArchiveRecords+7)
	for index := 0; index < maxArchiveRecords+7; index++ {
		records = append(records, Record{
			ID:          fmt.Sprintf("completion_%05d", index),
			Sequence:    uint64(index + 1),
			AgentID:     fmt.Sprintf("agent_%05d", index),
			WorkspaceID: item.ID,
			Status:      StatusCompleted,
			Title:       "done",
			Source:      "test",
			CreatedAt:   time.Unix(int64(index+1), 0).UTC(),
		})
	}
	if err := appendWorkspaceArchive(local, records); err != nil {
		t.Fatal(err)
	}
	archived, err := loadWorkspaceArchive(local)
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != maxArchiveRecords {
		t.Fatalf("archive records=%d want=%d", len(archived), maxArchiveRecords)
	}
	if archived[0].Sequence != 8 || archived[len(archived)-1].Sequence != uint64(maxArchiveRecords+7) {
		t.Fatalf("archive range=%d..%d", archived[0].Sequence, archived[len(archived)-1].Sequence)
	}
}

func TestCompletionStartupCompactsOversizedHotHistory(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	local := workspacestate.New(item.Path)
	records := make([]Record, 0, 4)
	for index := 1; index <= 4; index++ {
		records = append(records, Record{
			ID:          fmt.Sprintf("completion_startup_%d", index),
			Sequence:    uint64(index),
			AgentID:     DeriveAgentID("startup-caller", fmt.Sprintf("generation-%d", index)),
			WorkspaceID: item.ID,
			Status:      StatusPartial,
			Title:       fmt.Sprintf("Step %d", index),
			Source:      "mcp",
			CreatedAt:   time.Date(2026, 9, 25, 0, index, 0, 0, time.UTC),
		})
	}
	if err := saveWorkspaceHistory(local, workspaceHistory{Version: workspaceHistoryVersion, Records: records}); err != nil {
		t.Fatal(err)
	}

	service, err := NewWorkspaceService(manager, Options{MaxRecords: 2})
	if err != nil {
		t.Fatal(err)
	}
	hot := readTestHistory(t, item.Path)
	if len(hot.Records) != 2 || hot.Records[0].Sequence != 3 || hot.Records[1].Sequence != 4 {
		t.Fatalf("startup hot=%#v", hot.Records)
	}
	archived, err := loadWorkspaceArchive(local)
	if err != nil || len(archived) != 2 || archived[0].Sequence != 1 || archived[1].Sequence != 2 {
		t.Fatalf("startup archive=%#v err=%v", archived, err)
	}
	all, err := service.RecentWorkspace(item.ID, 10)
	if err != nil || len(all) != 4 || all[0].Sequence != 1 || all[3].Sequence != 4 {
		t.Fatalf("startup all=%#v err=%v", all, err)
	}
	if service.LatestSequence() != 4 {
		t.Fatalf("startup latest=%d", service.LatestSequence())
	}
	health := service.Diagnose(item.ID)
	if health.Status != HealthHealthy || health.HotRecords != 2 || health.ArchivedRecords != 2 || health.LatestSequence != 4 {
		t.Fatalf("startup health=%#v", health)
	}
}

func TestCompletionArchiveRepairsCorruptTailOnNextAppend(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	ids := []string{"completion_one", "completion_two", "completion_three"}
	idIndex := 0
	service, err := NewWorkspaceService(manager, Options{MaxRecords: 1, NewID: func() (string, error) {
		value := ids[idIndex]
		idIndex++
		return value, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		if _, created, err := service.Accept(
			Identity{AgentID: DeriveAgentID("caller", fmt.Sprintf("generation-%d", index)), Source: "mcp"},
			Input{WorkspaceID: item.ID, Status: StatusPartial, Title: fmt.Sprintf("Step %d", index)},
		); err != nil || !created {
			t.Fatalf("index=%d created=%t err=%v", index, created, err)
		}
	}
	local := workspacestate.New(item.Path)
	archivePath, err := workspaceArchivePath(local)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(archivePath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{broken}\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewWorkspaceService(workspace.NewManager(workspace.DefaultStorePath()), Options{MaxRecords: 1, NewID: func() (string, error) { return ids[2], nil }})
	if err != nil {
		t.Fatalf("restart rejected corrupt archive tail: %v", err)
	}
	health := restarted.Diagnose(item.ID)
	if health.Status != HealthDegraded || !health.ArchiveTailIssue || health.ArchivedRecords != 1 {
		t.Fatalf("health before repair=%#v", health)
	}
	third, created, err := restarted.Accept(
		Identity{AgentID: DeriveAgentID("caller", "generation-2"), Source: "mcp"},
		Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Finished"},
	)
	if err != nil || !created || third.Sequence != 3 {
		t.Fatalf("third=%#v created=%t err=%v", third, created, err)
	}
	health = restarted.Diagnose(item.ID)
	if health.Status != HealthHealthy || health.ArchiveTailIssue || health.ArchivedRecords != 2 || health.HotRecords != 1 {
		t.Fatalf("health after repair=%#v", health)
	}
	all, err := restarted.RecentWorkspace(item.ID, 10)
	if err != nil || len(all) != 3 || all[0].Sequence != 1 || all[2].Sequence != 3 {
		t.Fatalf("all=%#v err=%v", all, err)
	}
}

func TestCompletionCloseDisposesSubscriptionsAndRejectsAccept(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(workspace.DefaultStorePath())
	item := registerTestWorkspace(t, manager, t.TempDir())
	service, err := NewWorkspaceService(manager, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := service.SubscribeSnapshot(0)
	service.Close()
	if _, ok := <-sub.Events; ok {
		t.Fatal("completion events subscription remained open after close")
	}
	if _, ok := <-sub.Overflow; ok {
		t.Fatal("completion overflow subscription remained open after close")
	}
	closedSub, _ := service.SubscribeSnapshot(0)
	if _, ok := <-closedSub.Events; ok {
		t.Fatal("subscription created after close was not closed")
	}
	if _, created, err := service.Accept(
		Identity{AgentID: DeriveAgentID("caller", "generation"), Source: "mcp"},
		Input{WorkspaceID: item.ID, Status: StatusCompleted, Title: "Done"},
	); err == nil || created || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("accept after close created=%t err=%v", created, err)
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
