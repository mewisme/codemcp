package completion

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestMergeWorkspaceStatePreservesUniqueRecordsExactlyOnce(t *testing.T) {
	workspaceID := "ws_merge"
	registered := workspacestate.New(t.TempDir())
	destination := workspacestate.New(t.TempDir())
	output := workspacestate.New(t.TempDir())
	now := time.Now().UTC()
	shared := Record{ID: "completion_shared", Sequence: 2, AgentID: "0123456789abcdef", WorkspaceID: workspaceID, Status: StatusCompleted, Title: "shared", CreatedAt: now}
	left := Record{ID: "completion_left", Sequence: 1, AgentID: "0123456789abcdef", WorkspaceID: workspaceID, Status: StatusCompleted, Title: "left", CreatedAt: now.Add(-time.Minute)}
	right := Record{ID: "completion_right", Sequence: 3, AgentID: "fedcba9876543210", WorkspaceID: workspaceID, Status: StatusPartial, Title: "right", CreatedAt: now.Add(time.Minute)}
	if err := saveWorkspaceHistory(registered, workspaceHistory{Records: []Record{left, shared}}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceHistory(destination, workspaceHistory{Records: []Record{shared, right}}); err != nil {
		t.Fatal(err)
	}
	if err := MergeWorkspaceState(registered, destination, output, workspaceID); err != nil {
		t.Fatal(err)
	}
	records, err := strictWorkspaceRecords(output, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].ID != left.ID || records[1].ID != shared.ID || records[2].ID != right.ID {
		t.Fatalf("records=%#v", records)
	}
}

func TestMergeWorkspaceStateRejectsDivergentDuplicateID(t *testing.T) {
	workspaceID := "ws_merge"
	registered := workspacestate.New(t.TempDir())
	destination := workspacestate.New(t.TempDir())
	output := workspacestate.New(t.TempDir())
	base := Record{ID: "completion_same", Sequence: 1, AgentID: "0123456789abcdef", WorkspaceID: workspaceID, Status: StatusCompleted, Title: "left", CreatedAt: time.Now().UTC()}
	changed := base
	changed.Title = "right"
	if err := saveWorkspaceHistory(registered, workspaceHistory{Records: []Record{base}}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceHistory(destination, workspaceHistory{Records: []Record{changed}}); err != nil {
		t.Fatal(err)
	}
	if err := MergeWorkspaceState(registered, destination, output, workspaceID); !errors.Is(err, ErrWorkspaceMergeConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestMergedWorkspaceHistoryIsAcceptedByHealthModel(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	registered := workspacestate.New(t.TempDir())
	destination := workspacestate.New(t.TempDir())
	output := workspacestate.New(root)
	now := time.Now().UTC()
	left := Record{ID: "completion_left", Sequence: 1, AgentID: "0123456789abcdef", WorkspaceID: item.ID, Status: StatusCompleted, Title: "left", CreatedAt: now}
	right := Record{ID: "completion_right", Sequence: 2, AgentID: "fedcba9876543210", WorkspaceID: item.ID, Status: StatusCompleted, Title: "right", CreatedAt: now.Add(time.Second)}
	if err := saveWorkspaceHistory(registered, workspaceHistory{Records: []Record{left}}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceHistory(destination, workspaceHistory{Records: []Record{right}}); err != nil {
		t.Fatal(err)
	}
	if err := MergeWorkspaceState(registered, destination, output, item.ID); err != nil {
		t.Fatal(err)
	}
	service, err := NewWorkspaceService(manager, Options{SequencePath: filepath.Join(t.TempDir(), "sequence.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	health := service.Diagnose(item.ID)
	if health.Status != HealthHealthy || health.HotRecords != 2 || health.LatestSequence != 2 {
		t.Fatalf("health=%#v", health)
	}
}
