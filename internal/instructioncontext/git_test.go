package instructioncontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitutil "go.mewis.me/codemcp/internal/git"
)

func runGitTest(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	result, err := gitutil.OrThrow(context.Background(), cwd, args...)
	if err != nil {
		t.Fatal(err)
	}
	return result.Stdout
}

func initGitTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitTest(t, root, "init", "-b", "main")
	return root
}

func commitGitTestFile(t *testing.T, root, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", name)
	runGitTest(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", message)
}

func TestLoadGitSnapshotRepoStatusAndRecentCommits(t *testing.T) {
	root := initGitTestRepo(t)
	commitGitTestFile(t, root, "one.txt", "one", "first commit")
	commitGitTestFile(t, root, "two.txt", "two", "second commit")
	commitGitTestFile(t, root, "three.txt", "three", "third commit")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	snapshot := LoadGitSnapshot(context.Background(), root, GitSnapshotOptions{WorkspaceRoots: []string{root}})
	root = canonicalTestPath(t, root)
	if !snapshot.IsRepo || snapshot.Root != root || snapshot.Branch != "main" || snapshot.Error != "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !strings.Contains(snapshot.StatusShort, "## main") || !strings.Contains(snapshot.StatusShort, "dirty.txt") || snapshot.StatusTruncated {
		t.Fatalf("status = %#v", snapshot)
	}
	if len(snapshot.RecentCommits) != 3 || !strings.Contains(snapshot.RecentCommits[0], "third commit") || !strings.Contains(snapshot.RecentCommits[2], "first commit") {
		t.Fatalf("commits = %#v", snapshot.RecentCommits)
	}
}

func TestLoadGitSnapshotDetachedHead(t *testing.T) {
	root := initGitTestRepo(t)
	commitGitTestFile(t, root, "one.txt", "one", "first commit")
	runGitTest(t, root, "checkout", "--detach", "HEAD")

	snapshot := LoadGitSnapshot(context.Background(), root, GitSnapshotOptions{WorkspaceRoots: []string{root}})
	if !snapshot.IsRepo || snapshot.Branch != "" || snapshot.Error != "" || len(snapshot.RecentCommits) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLoadGitSnapshotNonRepoIsFailSoft(t *testing.T) {
	root := t.TempDir()
	snapshot := LoadGitSnapshot(context.Background(), root, GitSnapshotOptions{WorkspaceRoots: []string{root}})
	if snapshot.IsRepo || snapshot.Error != "" || snapshot.Root != "" || len(snapshot.RecentCommits) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLoadGitSnapshotRejectsRepositoryOutsideWorkspaceRoots(t *testing.T) {
	repo := initGitTestRepo(t)
	commitGitTestFile(t, repo, "one.txt", "one", "first commit")
	subdir := filepath.Join(repo, "nested")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	snapshot := LoadGitSnapshot(context.Background(), subdir, GitSnapshotOptions{WorkspaceRoots: []string{subdir}})
	if snapshot.IsRepo || !strings.Contains(snapshot.Error, "outside effective workspace roots") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestLoadGitSnapshotTruncatesStatusUTF8Safely(t *testing.T) {
	root := initGitTestRepo(t)
	commitGitTestFile(t, root, "base.txt", "base", "base commit")
	name := "界界界.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	snapshot := LoadGitSnapshot(context.Background(), root, GitSnapshotOptions{WorkspaceRoots: []string{root}, MaxStatusBytes: 15})
	if !snapshot.IsRepo || !snapshot.StatusTruncated || len([]byte(snapshot.StatusShort)) > 15 || !strings.HasPrefix(snapshot.StatusShort, "## main") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
