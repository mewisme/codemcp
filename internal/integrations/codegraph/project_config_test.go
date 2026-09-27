package codegraph

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestReconcileProjectConfigUsesEffectiveGitIgnoreScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".local/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "frontend", ".gitignore"), []byte("/node_modules/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	infoExclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.WriteFile(infoExclude, []byte(".cm/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(root, ".local", "worktree", "copy.go"):           "package copy\n",
		filepath.Join(root, "frontend", "node_modules", "pkg", "x.js"): "export const x = 1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, projectConfigFileName)
	if err := os.WriteFile(configPath, []byte("{\n  \"extensions\": {\".tpl\": \"php\"},\n  \"exclude\": [\"user/\"]\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := workspacestate.New(root)
	changed, err := ReconcileProjectConfig(context.Background(), store, "ws_test", root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("config unexpectedly unchanged")
	}
	config := readCodeGraphConfigForTest(t, configPath)
	for _, expected := range []string{"user/", projectConfigFileName, ".cm/", ".local/", "frontend/node_modules/"} {
		if !slices.Contains(config.Exclude, expected) {
			t.Fatalf("exclude=%v missing %q", config.Exclude, expected)
		}
	}
	checkIgnored := exec.Command("git", "check-ignore", "--quiet", "--", projectConfigFileName)
	checkIgnored.Dir = root
	if err := checkIgnored.Run(); err != nil {
		t.Fatalf("%s is not excluded from Git: %v", projectConfigFileName, err)
	}
	if config.Extensions[".tpl"] != "php" {
		t.Fatalf("extensions not preserved: %#v", config.Extensions)
	}
	metadata, err := loadWorkspaceMetadata(store, "ws_test")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(metadata.Projects["."].ManagedExclude, projectConfigFileName) {
		t.Fatalf("managed excludes=%v missing %q", metadata.Projects["."].ManagedExclude, projectConfigFileName)
	}

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	changed, err = ReconcileProjectConfig(context.Background(), store, "ws_test", root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("removed ignore did not update config")
	}
	config = readCodeGraphConfigForTest(t, configPath)
	if slices.Contains(config.Exclude, ".local/") {
		t.Fatalf("stale managed exclude retained: %v", config.Exclude)
	}
	for _, expected := range []string{"user/", projectConfigFileName, ".cm/", "frontend/node_modules/"} {
		if !slices.Contains(config.Exclude, expected) {
			t.Fatalf("exclude=%v missing %q after reconcile", config.Exclude, expected)
		}
	}
}

func TestReconcileProjectConfigAlwaysExcludesItsOwnConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	configPath := filepath.Join(root, projectConfigFileName)
	if err := os.WriteFile(configPath, []byte("{\"include\":[\"*.json\"]}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := workspacestate.New(root)
	if _, err := ReconcileProjectConfig(context.Background(), store, "ws_test", root, "."); err != nil {
		t.Fatal(err)
	}
	config := readCodeGraphConfigForTest(t, configPath)
	if !slices.Contains(config.Exclude, projectConfigFileName) {
		t.Fatalf("exclude=%v missing own config", config.Exclude)
	}
}

func TestReconcileProjectConfigDoesNotOverrideExplicitInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("generated/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "generated", "source"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "generated", "source", "source.go"), []byte("package generated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, projectConfigFileName)
	if err := os.WriteFile(configPath, []byte("{\"include\":[\"generated/source/\"]}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := workspacestate.New(root)
	if _, err := ReconcileProjectConfig(context.Background(), store, "ws_test", root, "."); err != nil {
		t.Fatal(err)
	}
	config := readCodeGraphConfigForTest(t, configPath)
	if slices.Contains(config.Exclude, "generated/") {
		t.Fatalf("explicit include was overridden: %#v", config)
	}
	if !ProjectConfigRequiresConservativeSync(root) {
		t.Fatal("explicit include did not require conservative sync")
	}
}

func TestReconcileProjectConfigDoesNotMaskIncludedIgnoredChildRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("packages/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "packages", "service-a")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	childGit := exec.Command("git", "init", "-q")
	childGit.Dir = child
	if output, err := childGit.CombinedOutput(); err != nil {
		t.Fatalf("child git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(child, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, projectConfigFileName)
	if err := os.WriteFile(configPath, []byte("{\"includeIgnored\":[\"packages/service-a/\"]}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	store := workspacestate.New(root)
	if _, err := ReconcileProjectConfig(context.Background(), store, "ws_test", root, "."); err != nil {
		t.Fatal(err)
	}
	config := readCodeGraphConfigForTest(t, configPath)
	if slices.Contains(config.Exclude, "packages/") {
		t.Fatalf("includeIgnored child was masked by parent exclude: %#v", config)
	}
	if !ProjectConfigRequiresConservativeSync(root) {
		t.Fatal("includeIgnored did not require conservative sync")
	}
}

func TestWorkspaceFingerprintIgnoresGitExcludedTrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".local/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, ".local", "worktree", "copy.go")
	if err := os.MkdirAll(filepath.Dir(ignored), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignored, []byte("package copy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	store := workspacestate.New(root)
	status := Status{Enabled: true, Resolution: Resolution{Source: ExecutableSystem, Path: "/tmp/codegraph", Verified: true}}
	if err := RecordWorkspaceLifecycle(store, "ws_test", root, ".", "sync", testTime()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ignored, []byte("package copy\nvar changed = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := InspectWorkspace(status, store, "ws_test", root, "."); got.Freshness != FreshnessFresh {
		t.Fatalf("ignored tree dirtied fingerprint: %#v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nvar changed = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := InspectWorkspace(status, store, "ws_test", root, "."); got.Freshness != FreshnessDirty || got.DirtyReason != "source_changed" {
		t.Fatalf("visible source change not detected: %#v", got)
	}
}

type codeGraphConfigFixture struct {
	Exclude    []string          `json:"exclude"`
	Include    []string          `json:"include"`
	Extensions map[string]string `json:"extensions"`
}

func readCodeGraphConfigForTest(t *testing.T, path string) codeGraphConfigFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config codeGraphConfigFixture
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func gitInit(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

func testTime() (value time.Time) {
	return time.Unix(100, 0)
}
