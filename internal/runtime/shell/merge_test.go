package shell

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	statepkg "go.mewis.me/codemcp/internal/state"
)

func TestMergeSessionStateRewritesSourceCWDAndUsesFreshestValidState(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	registeredPath := filepath.Join(t.TempDir(), "registered.json")
	destinationPath := filepath.Join(t.TempDir(), "destination.json")
	outputPath := filepath.Join(t.TempDir(), "merged.json")
	older := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	newer := time.Now().UTC().Format(time.RFC3339Nano)
	if err := statepkg.WriteJSONAtomic(registeredPath, SessionState{
		Version: sessionStateVersion, WorkspaceID: "ws_test", CWD: filepath.Join(source, "sub"), UpdatedAt: newer,
	}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := statepkg.WriteJSONAtomic(destinationPath, SessionState{
		Version: sessionStateVersion, WorkspaceID: "ws_test", CWD: destination, UpdatedAt: older,
	}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := MergeSessionState(registeredPath, destinationPath, outputPath, "ws_test", source, destination, nil); err != nil {
		t.Fatal(err)
	}
	merged, ok, err := loadMergeSession(outputPath, "ws_test")
	if err != nil || !ok {
		t.Fatalf("merged=%#v ok=%v err=%v", merged, ok, err)
	}
	if merged.CWD != filepath.Join(destination, "sub") {
		t.Fatalf("cwd=%q", merged.CWD)
	}
}

func TestMergeSessionStateRejectsTieAndEscapingCWD(t *testing.T) {
	destination := t.TempDir()
	source := t.TempDir()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	left := filepath.Join(t.TempDir(), "left.json")
	right := filepath.Join(t.TempDir(), "right.json")
	out := filepath.Join(t.TempDir(), "out.json")
	if err := statepkg.WriteJSONAtomic(left, SessionState{Version: 1, WorkspaceID: "ws", CWD: source, UpdatedAt: stamp, RecentCommands: []string{"left"}}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := statepkg.WriteJSONAtomic(right, SessionState{Version: 1, WorkspaceID: "ws", CWD: destination, UpdatedAt: stamp, RecentCommands: []string{"right"}}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := MergeSessionState(left, right, out, "ws", source, destination, nil); !errors.Is(err, ErrSessionMergeConflict) {
		t.Fatalf("tie err=%v", err)
	}

	escape := t.TempDir()
	if err := statepkg.WriteJSONAtomic(right, SessionState{Version: 1, WorkspaceID: "ws", CWD: escape, UpdatedAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := MergeSessionState("", right, out, "ws", source, destination, nil); err == nil {
		t.Fatal("expected escaping cwd to fail")
	}
}
