package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestGitHygieneRegistrationIsIdempotentAndPreservesRootGitignore(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	rootIgnore := filepath.Join(root, ".gitignore")
	const rootIgnoreContent = "dist/\n"
	if err := os.WriteFile(rootIgnore, []byte(rootIgnoreContent), 0644); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	if _, err := manager.Register(root); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, LocalDirName, ".gitignore")
	exclude := filepath.Join(root, ".git", "info", "exclude")
	nestedBefore, err := os.ReadFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	excludeBefore, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Register(root); err != nil {
		t.Fatal(err)
	}
	nestedAfter, err := os.ReadFile(nested)
	if err != nil {
		t.Fatal(err)
	}
	excludeAfter, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(nestedBefore, nestedAfter) || !bytes.Equal(excludeBefore, excludeAfter) {
		t.Fatalf("repeated registration changed hygiene bytes: nested=%q/%q exclude=%q/%q", nestedBefore, nestedAfter, excludeBefore, excludeAfter)
	}
	if strings.Count(string(nestedAfter), "*\n") != 1 || strings.Count(string(excludeAfter), ".cm/\n") != 1 {
		t.Fatalf("duplicate hygiene rules: nested=%q exclude=%q", nestedAfter, excludeAfter)
	}
	rootIgnoreAfter, err := os.ReadFile(rootIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if string(rootIgnoreAfter) != rootIgnoreContent {
		t.Fatalf("tracked root .gitignore changed: %q", rootIgnoreAfter)
	}
}

func TestGitHygieneConcurrentRepairIsByteStable(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	if _, _, err := workspacestate.New(root).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	first := EnsureLocalStateGitHygiene(root)
	if err := first.Error(); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, LocalDirName, ".gitignore")
	exclude := filepath.Join(root, ".git", "info", "exclude")
	nestedBefore, _ := os.ReadFile(nested)
	excludeBefore, _ := os.ReadFile(exclude)

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := EnsureLocalStateGitHygiene(root)
			errs <- result.Error()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	nestedAfter, _ := os.ReadFile(nested)
	excludeAfter, _ := os.ReadFile(exclude)
	if !bytes.Equal(nestedBefore, nestedAfter) || !bytes.Equal(excludeBefore, excludeAfter) {
		t.Fatalf("concurrent repair changed bytes")
	}
}

func TestGitHygieneLinkedWorktreeUsesCommonInfoExclude(t *testing.T) {
	root := t.TempDir()
	common := t.TempDir()
	gitdir := filepath.Join(common, "worktrees", "fixture")
	if err := os.MkdirAll(gitdir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "HEAD"), []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspacestate.New(root).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	result := EnsureLocalStateGitHygiene(root)
	if err := result.Error(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(common, "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsGitExcludeRuleForTest(string(data)) {
		t.Fatalf("common info/exclude=%q", data)
	}
}

func TestGitHygieneNestedRepositoryIsNotMutated(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	child := filepath.Join(root, "nested")
	if err := os.MkdirAll(child, 0755); err != nil {
		t.Fatal(err)
	}
	initGitFixture(t, child)
	childExclude := filepath.Join(child, ".git", "info", "exclude")
	const childContent = "# child only\n"
	if err := os.WriteFile(childExclude, []byte(childContent), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspacestate.New(root).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLocalStateGitHygiene(root).Error(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(childExclude)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != childContent {
		t.Fatalf("nested repository exclude changed: %q", got)
	}
}

func TestGitHygieneRejectsSymlinkedExcludeWithoutMutatingTarget(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink creation may require Windows Developer Mode or elevation")
	}
	root := t.TempDir()
	initGitFixture(t, root)
	outside := filepath.Join(t.TempDir(), "exclude")
	const outsideContent = "outside\n"
	if err := os.WriteFile(outside, []byte(outsideContent), 0644); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.Remove(exclude); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, exclude); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := workspacestate.New(root).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	result := EnsureLocalStateGitHygiene(root)
	if result.GitExclude.State != GitHygieneFailed || !result.Protected() {
		t.Fatalf("symlinked exclude result=%#v", result)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != outsideContent {
		t.Fatalf("outside exclude changed: data=%q err=%v", data, err)
	}
}

func TestGitHygieneActivationRepairsDeletedRules(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	manager := newTestManager(t)
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, LocalDirName, ".gitignore")
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte("# reset\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(); err != nil {
		t.Fatal(err)
	}
	defer manager.Deactivate()
	diagnostic := InspectLocalStateGitHygiene(context.Background(), root)
	if !diagnostic.Protected || diagnostic.NestedIgnore != GitHygieneHealthy || diagnostic.GitExclude != GitHygieneHealthy {
		t.Fatalf("repaired diagnostic=%#v", diagnostic)
	}
	runtimeDiagnostic := manager.RuntimeDiagnostics()
	if len(runtimeDiagnostic.Workspaces) != 1 || runtimeDiagnostic.Workspaces[0].WorkspaceID != item.ID || !runtimeDiagnostic.Workspaces[0].GitHygiene.Protected {
		t.Fatalf("runtime diagnostics=%#v", runtimeDiagnostic)
	}
}

func TestGitHygieneDetectsTrackedLocalStateWithoutMutatingIndex(t *testing.T) {
	root := t.TempDir()
	initGitFixture(t, root)
	if _, _, err := workspacestate.New(root).EnsureIdentity(""); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLocalStateGitHygiene(root).Error(); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, LocalDirName, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, root, "add", "-f", ".cm/tracked.txt")
	before := strings.TrimSpace(runGitFixture(t, root, "ls-files", "--", ".cm"))
	diagnostic := InspectLocalStateGitHygiene(context.Background(), root)
	if !diagnostic.Tracked || diagnostic.Index != GitIndexTracked || diagnostic.Guidance != GitTrackedGuidance {
		t.Fatalf("tracked diagnostic=%#v", diagnostic)
	}
	after := strings.TrimSpace(runGitFixture(t, root, "ls-files", "--", ".cm"))
	if before != after || !strings.Contains(after, ".cm/tracked.txt") {
		t.Fatalf("inspection mutated Git index: before=%q after=%q", before, after)
	}
}

func initGitFixture(t *testing.T, root string) {
	t.Helper()
	runGitFixture(t, root, "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0755); err != nil {
		t.Fatal(err)
	}
}

func runGitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}

func containsGitExcludeRuleForTest(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == ".cm/" || line == "/.cm/" {
			return true
		}
	}
	return false
}
