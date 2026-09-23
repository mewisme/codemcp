package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
}

func TestRegisterIsStableAndPersistent(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t)
	first, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Path != second.Path {
		t.Fatalf("unstable registration: %#v %#v", first, second)
	}
	reloaded := NewManager(manager.path)
	got, err := reloaded.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("persisted workspace = %#v, want %#v", got, first)
	}
}

func TestWorkspaceLifecycleEmitsDeepTrace(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	root := t.TempDir()
	allowed := t.TempDir()
	events := []tracepkg.Event{}
	manager := NewManager(store).SetTraceObserver(func(event tracepkg.Event) { events = append(events, event) })
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddAllowDir(item.ID, allowed); err != nil {
		t.Fatal(err)
	}
	container, err := manager.CreateContainer("trace")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddWorkspaceToContainer(container.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unregister(item.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"workspace.registry.load.completed",
		"workspace.path.resolve.completed",
		"workspace.registry.persist.completed",
		"workspace.register.completed",
		"workspace.allow-dir.add.completed",
		"workspace.container.create.completed",
		"workspace.container.membership.completed",
		"workspace.unregister.completed",
	} {
		if !workspaceTraceContains(events, name) {
			t.Fatalf("missing trace event %s: %#v", name, events)
		}
	}
	for _, event := range events {
		if strings.HasSuffix(event.Name, ".started") && !workspaceTraceHasTerminal(events, event.Name) {
			t.Fatalf("trace span never terminated: %s", event.Name)
		}
	}
}

func workspaceTraceContains(events []tracepkg.Event, name string) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func workspaceTraceHasTerminal(events []tracepkg.Event, startName string) bool {
	base := strings.TrimSuffix(startName, ".started")
	return workspaceTraceContains(events, base+".completed") || workspaceTraceContains(events, base+".failed")
}

func TestReloadAppliesExternalRegistryChangesAndPreservesRuntimeSettings(t *testing.T) {
	store := filepath.Join(t.TempDir(), "workspaces.json")
	workspaceRoot := t.TempDir()
	globalAllow := t.TempDir()
	shellPath := []string{t.TempDir()}
	runtimeManager := NewManagerWithGlobalAllowDirs(store, []string{globalAllow})
	runtimeManager.SetShellPath(shellPath)
	if items, err := runtimeManager.List(); err != nil || len(items) != 0 {
		t.Fatalf("initial items=%#v err=%v", items, err)
	}

	external := NewManager(store)
	item, err := external.Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeManager.Get(item.ID); err == nil {
		t.Fatal("externally registered workspace became visible before reload")
	}
	if err := runtimeManager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeManager.Get(item.ID); err != nil {
		t.Fatalf("reloaded workspace unavailable: %v", err)
	}
	roots, err := runtimeManager.EffectiveRoots(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	canonicalGlobalAllow, err := canonicalExistingDirectory(globalAllow)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(roots, canonicalGlobalAllow) {
		t.Fatalf("global allow dirs lost after reload: %#v", roots)
	}
	if got := runtimeManager.ShellPath(); !reflect.DeepEqual(got, shellPath) {
		t.Fatalf("shell path after reload=%#v want=%#v", got, shellPath)
	}

	if err := external.Unregister(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := runtimeManager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeManager.Get(item.ID); err == nil {
		t.Fatal("externally unregistered workspace remained visible after reload")
	}
}

func TestResolveDirectoryRejectsEscape(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ResolveDirectory(item.ID, filepath.Dir(root)); err == nil {
		t.Fatal("expected working directory escape to be rejected")
	}
}

func TestResolvePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResolvePath(item.ID, root, filepath.Join("outside-link", "file.txt"), false); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestOpenRootForPathRejectsSymlinkSwapEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	safe := filepath.Join(root, "safe")
	if err := os.Mkdir(safe, 0755); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolvePath(item.ID, root, filepath.Join("safe", "file.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(safe); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, safe); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rootHandle, relative, err := manager.OpenRootForPath(item.ID, resolved)
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("outside file was created: %v", statErr)
		}
		return
	}
	defer rootHandle.Close()
	if err := rootHandle.WriteFile(relative, []byte("escape"), 0644); err == nil {
		t.Fatal("rooted write followed swapped symlink outside workspace")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file was created: %v", err)
	}
}

func TestOpenRootForPathUsesAllowedDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddAllowDir(item.ID, allowed); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(allowed, "nested", "file.txt")
	rootHandle, relative, err := manager.OpenRootForPath(item.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rootHandle.Close()
	wantRoot := canonicalRoot(allowed)
	if rootHandle.Name() != wantRoot {
		t.Fatalf("root=%q want=%q", rootHandle.Name(), wantRoot)
	}
	if relative != filepath.Join("nested", "file.txt") {
		t.Fatalf("relative=%q", relative)
	}
}

func TestOpenRootForPathAcceptsCanonicalWorkspaceThroughSymlinkedParent(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	parent := t.TempDir()
	canonicalParent := filepath.Join(parent, "canonical")
	aliasParent := filepath.Join(parent, "alias")
	root := filepath.Join(canonicalParent, "workspace")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonicalParent, aliasParent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(filepath.Join(aliasParent, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	rootHandle, relative, err := manager.OpenRootForPath(item.ID, filepath.Join(aliasParent, "workspace", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer rootHandle.Close()
	wantRoot := canonicalRoot(root)
	if rootHandle.Name() != wantRoot {
		t.Fatalf("root=%q want=%q", rootHandle.Name(), wantRoot)
	}
	if relative != "file.txt" {
		t.Fatalf("relative=%q", relative)
	}
}

func TestOpenRootForPathRejectsReplacedWorkspaceRootSymlink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if handle, _, err := manager.OpenRootForPath(item.ID, filepath.Join(root, "file.txt")); err == nil {
		_ = handle.Close()
		t.Fatal("expected replaced workspace root symlink to be rejected")
	}
}

func TestMutationGuardAllowsWorkspaceLocalRm(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateMutationCommand(item.ID, root, "rm file.txt"); err != nil {
		t.Fatalf("safe rm rejected: %v", err)
	}
}

func TestMutationGuardRejectsCwdChange(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ValidateMutationCommand(item.ID, root, "cd child && rm file.txt")
	if err == nil || !strings.Contains(err.Error(), "cwd change") {
		t.Fatalf("error = %v, want cwd change denial", err)
	}
}

func TestMutationGuardRejectsPopdMutation(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ValidateMutationCommand(item.ID, root, "popd && rm file.txt")
	if err == nil || !strings.Contains(err.Error(), "popd cannot be proven workspace-safe") {
		t.Fatalf("error = %v, want popd fail-closed denial", err)
	}
}

func TestMutationGuardRejectsTargetlessPushdMutation(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ValidateMutationCommand(item.ID, root, "pushd && rm file.txt")
	if err == nil || !strings.Contains(err.Error(), "pushd requires an explicit target") {
		t.Fatalf("error = %v, want targetless pushd denial", err)
	}
}

func TestMutationGuardRejectsOutsidePath(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "file.txt")
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateMutationCommand(item.ID, root, "rm "+outside); err == nil {
		t.Fatal("expected outside rm to be rejected")
	}
}

func TestMutationGuardValidatesNestedShellMutation(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateMutationCommand(item.ID, root, `bash -lc "rm file.txt"`); err != nil {
		t.Fatalf("workspace-safe nested mutation rejected: %v", err)
	}
	err = manager.ValidateMutationCommand(item.ID, root, `bash -lc "rm `+outside+`"`)
	if err == nil || !strings.Contains(err.Error(), "nested bash mutation") {
		t.Fatalf("outside nested mutation error = %v", err)
	}
}

func TestMutationGuardAllowsWorkspaceLocalMove(t *testing.T) {
	root := t.TempDir()
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateMutationCommand(item.ID, root, "mv old.txt new.txt"); err != nil {
		t.Fatalf("safe mv rejected: %v", err)
	}
}

func TestMutationGuardRejectsMoveDestinationOutside(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "new.txt")
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ValidateMutationCommand(item.ID, root, "mv old.txt "+outside); err == nil {
		t.Fatal("expected outside mv destination to be rejected")
	}
}

func TestGlobalAllowDirExtendsWorkspaceScope(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	outside := t.TempDir()
	manager := NewManagerWithGlobalAllowDirs(filepath.Join(t.TempDir(), "workspaces.json"), []string{allowed})
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedAllowed := canonicalRoot(allowed)
	if _, cwd, err := manager.ResolveDirectory(item.ID, allowed); err != nil || cwd != expectedAllowed {
		t.Fatalf("allowed cwd = %q, want %q err=%v", cwd, expectedAllowed, err)
	}
	path := filepath.Join(allowed, "artifact.txt")
	expectedPath, err := canonicalForContainment(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manager.ResolvePath(item.ID, allowed, path, false); err != nil || got != expectedPath {
		t.Fatalf("allowed path = %q, want %q err=%v", got, expectedPath, err)
	}
	if _, err := manager.ResolvePath(item.ID, root, filepath.Join(outside, "escape.txt"), false); err == nil {
		t.Fatal("unlisted outside path was allowed")
	}
}

func TestWorkspaceAllowDirPersistsAndRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	outside := t.TempDir()
	store := filepath.Join(t.TempDir(), "workspaces.json")
	manager := NewManager(store)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	item, err = manager.AddAllowDir(item.ID, allowed)
	if err != nil {
		t.Fatal(err)
	}
	expectedAllowed := canonicalRoot(allowed)
	if len(item.AllowDirs) != 1 || item.AllowDirs[0] != expectedAllowed {
		t.Fatalf("allow dirs = %#v, want %q", item.AllowDirs, expectedAllowed)
	}
	reloaded := NewManager(store)
	if _, _, err := reloaded.ResolveDirectory(item.ID, allowed); err != nil {
		t.Fatalf("persisted allow dir rejected: %v", err)
	}
	link := filepath.Join(allowed, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := reloaded.ResolvePath(item.ID, allowed, filepath.Join(link, "file.txt"), false); err == nil {
		t.Fatal("symlink escape from allowed dir was accepted")
	}
	if _, err := reloaded.RemoveAllowDir(item.ID, allowed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reloaded.ResolveDirectory(item.ID, allowed); err == nil {
		t.Fatal("removed allow dir remained accessible")
	}
}

func TestControlPlaneStateIsExcludedFromWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	controlPlane := filepath.Join(home, ".cm")
	workspaceRoot := filepath.Join(home, "project")
	if err := os.MkdirAll(controlPlane, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(filepath.Join(controlPlane, "workspaces.json"))
	manager.protectedRoot = canonicalRoot(controlPlane)
	if _, err := manager.Register(home); err == nil {
		t.Fatal("home workspace was accepted even though its .cm aliases global state")
	}
	item, err := manager.Register(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(workspaceRoot, "project.txt")
	expectedRegular, err := canonicalForContainment(regular, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := manager.ResolvePath(item.ID, workspaceRoot, regular, false); err != nil || got != expectedRegular {
		t.Fatalf("normal workspace path = %q, want %q err=%v", got, expectedRegular, err)
	}
	for _, path := range []string{controlPlane, filepath.Join(controlPlane, "config.json"), filepath.Join(controlPlane, "tunnel.json")} {
		if _, err := manager.ResolvePath(item.ID, workspaceRoot, path, false); err == nil {
			t.Fatalf("protected control-plane path was accessible: %s", path)
		}
	}
	if _, _, err := manager.ResolveDirectory(item.ID, controlPlane); err == nil {
		t.Fatal("protected control-plane directory was accepted as working directory")
	}
	if _, err := manager.AddAllowDir(item.ID, controlPlane); err == nil {
		t.Fatal("protected control-plane directory was accepted as workspace access grant")
	}
	if _, err := manager.Register(controlPlane); err == nil {
		t.Fatal("protected control-plane directory was accepted as workspace root")
	}
}

func TestDefaultStoreManagerProtectsActiveConfigRoot(t *testing.T) {
	defer configformat.SetRootPath("")
	root := t.TempDir()
	if err := configformat.SetRootPath(root); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(DefaultStorePath())
	if manager.protectedRoot != canonicalRoot(root) {
		t.Fatalf("protected root = %q, want %q", manager.protectedRoot, canonicalRoot(root))
	}
}
