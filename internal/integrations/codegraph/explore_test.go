package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/workspace"
)

func TestNormalizeExploreInputBoundsAndScope(t *testing.T) {
	valid, err := NormalizeExploreInput(" ws_test ", " find Foo ", "src/../pkg")
	if err != nil {
		t.Fatal(err)
	}
	if valid.WorkspaceID != "ws_test" || valid.Query != "find Foo" || valid.Path != "pkg" {
		t.Fatalf("normalized=%#v", valid)
	}

	tests := []struct {
		name      string
		workspace string
		query     string
		path      string
	}{
		{name: "missing workspace", query: "find Foo"},
		{name: "blank query", workspace: "ws_test", query: "   "},
		{name: "query too large", workspace: "ws_test", query: strings.Repeat("x", MaxQueryBytes+1)},
		{name: "query nul", workspace: "ws_test", query: "find\x00Foo"},
		{name: "absolute path", workspace: "ws_test", query: "find Foo", path: filepath.Join(string(filepath.Separator), "tmp")},
		{name: "escaping path", workspace: "ws_test", query: "find Foo", path: "../outside"},
		{name: "path too large", workspace: "ws_test", query: "find Foo", path: strings.Repeat("p", MaxProjectPathBytes+1)},
		{name: "path nul", workspace: "ws_test", query: "find Foo", path: "src\x00pkg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NormalizeExploreInput(test.workspace, test.query, test.path); err == nil {
				t.Fatalf("input unexpectedly accepted: %#v", test)
			}
		})
	}
}

func TestExploreStatesAreActionable(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}

	disabled, err := Explore(context.Background(), New(Options{Enabled: false}), manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       "find Foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.State != StateDisabled || disabled.Guidance == "" {
		t.Fatalf("disabled=%#v", disabled)
	}

	unavailableRuntime := New(Options{Enabled: true, ManagedRoot: t.TempDir()})
	unavailableRuntime.lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	unavailableRuntime.goos, unavailableRuntime.goarch = "unsupported", "unsupported"
	unavailable, err := Explore(context.Background(), unavailableRuntime, manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       "find Foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.State != StateUnavailable || unavailable.Guidance == "" {
		t.Fatalf("unavailable=%#v", unavailable)
	}

	executable := testExecutable(t, "codegraph")
	unindexed, err := Explore(context.Background(), New(Options{Enabled: true, ConfiguredPath: executable}), manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       "find Foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unindexed.State != StateUnindexed || unindexed.Guidance == "" {
		t.Fatalf("unindexed=%#v", unindexed)
	}
}

func TestExploreRefreshesDirtyIndexAndPassesQueryAsArgument(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(project, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "should-not-exist")
	executable := writeExploreFixture(t)
	query := "find Foo; touch " + marker
	value := New(Options{Enabled: true, ConfiguredPath: executable})
	state, err := Explore(context.Background(), value, manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       query,
		Path:        "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.State != StateReady || state.Output != "explored:"+query || state.ProjectPath != project {
		t.Fatalf("state=%#v", state)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("query was interpreted by a shell: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, ".codegraph", "sync-count"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "sync") != 1 {
		t.Fatalf("sync count=%q", data)
	}
	store, err := manager.LocalState(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := value.Status()
	if err != nil {
		t.Fatal(err)
	}
	workspaceStatus := InspectWorkspace(status, store, item.ID, project, "project")
	if workspaceStatus.Freshness != FreshnessFresh || workspaceStatus.IndexState != IndexIndexed {
		t.Fatalf("workspace status=%#v", workspaceStatus)
	}
}

func TestExploreDoesNotExposeProcessInternalsOnRefreshFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	root := t.TempDir()
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "codegraph")
	script := "#!/bin/sh\nprintf 'sensitive-process-detail:%s\\n' \"$0\" >&2\nexit 7\n"
	if err := os.WriteFile(executable, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	_, err = Explore(context.Background(), New(Options{Enabled: true, ConfiguredPath: executable}), manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       "find Foo",
	})
	if err == nil {
		t.Fatal("refresh failure unexpectedly succeeded")
	}
	message := err.Error()
	if message != "CodeGraph could not refresh the workspace index before exploration" {
		t.Fatalf("unexpected public diagnostic: %q", message)
	}
	if strings.Contains(message, executable) || strings.Contains(message, "sensitive-process-detail") || strings.Contains(message, "exit") {
		t.Fatalf("process internals leaked: %q", message)
	}
}

func TestExploreRejectsRegisteredRootEscapeEvenWhenGloballyAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	outside := t.TempDir()
	manager := workspace.NewManagerWithGlobalAllowDirs(filepath.Join(t.TempDir(), "workspaces.json"), []string{outside})
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	_, err = Explore(context.Background(), New(Options{Enabled: false}), manager, ExploreInput{
		WorkspaceID: item.ID,
		Query:       "find Foo",
		Path:        "linked",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "registered workspace root") {
		t.Fatalf("escape err=%v", err)
	}
}

func writeExploreFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codegraph")
	script := `#!/bin/sh
case "$1" in
  sync)
    project="$2"
    mkdir -p "$project/.codegraph"
    printf 'sync\n' >> "$project/.codegraph/sync-count"
    ;;
  explore)
    printf 'explored:%s\n' "$2"
    ;;
  --version)
    printf 'codegraph 1.6.0\n'
    ;;
  *)
    printf 'unexpected command\n' >&2
    exit 9
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
