package instructioncontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gitutil "go.mewis.me/codemcp/internal/git"
	"go.mewis.me/codemcp/internal/memory"
)

func TestBuildAssemblesInstructionContext(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	memoryRoot := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "primary agents")
	writeInstructionFile(t, filepath.Join(root, "CLAUDE.md"), "claude fallback")
	writeRuleFile(t, root, ".agents", "global.md", "global rule")
	writeSkillFile(t, root, ".agents", "release", "release", "Release workflow", "skill body must stay out")
	store := memory.NewStore(memoryRoot)
	if _, err := store.Upsert("ws_test", "tooling", "package-manager", "use pnpm"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitutil.OrThrow(context.Background(), root, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitutil.OrThrow(context.Background(), root, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitutil.OrThrow(context.Background(), root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	loadedAt := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	value, err := Build(context.Background(), BuildOptions{
		Root: root, WorkspaceID: "ws_test", WorkspaceRoot: root, CWD: root, WorkspaceRoots: []string{root}, MemoryStore: store,
		Memory: MemoryLoadOptions{HomeDir: home, MaxBytesPerSection: 25_000}, ToolProfile: ToolProfile{Name: "full", Count: 54},
		AdminEnabled: true, AdminPort: 37422, Now: func() time.Time { return loadedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	root = canonicalTestPath(t, root)
	if value.Root != root || value.WorkspaceID != "ws_test" || value.LoadedAt != loadedAt || value.InstructionBytes != len([]byte(value.InstructionsText)) {
		t.Fatalf("value = %#v", value)
	}
	if !value.Git.IsRepo || value.Git.Branch != "main" || len(value.Git.RecentCommits) != 1 {
		t.Fatalf("git = %#v", value.Git)
	}
	if len(value.ProjectMemory.Sections) != 2 || value.ProjectMemory.Sections[0].Source != "agents" || value.ProjectMemory.Sections[1].Source != "claude" {
		t.Fatalf("memory = %#v", value.ProjectMemory)
	}
	if !value.AutoMemory.Loaded || len(value.Rules) != 1 || len(value.Skills) != 4 {
		t.Fatalf("assembled context = %#v", value)
	}
	for _, expected := range []string{"primary agents", "claude fallback", "global rule", "Release workflow", "use pnpm"} {
		if !strings.Contains(value.InstructionsText, expected) {
			t.Fatalf("instructions missing %q:\n%s", expected, value.InstructionsText)
		}
	}
	if strings.Contains(value.InstructionsText, "skill body must stay out") {
		t.Fatalf("skill body leaked into instructions:\n%s", value.InstructionsText)
	}
}

func TestBuildRejectsProjectRootOutsideWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_, err := Build(context.Background(), BuildOptions{Root: outside, WorkspaceID: "ws_test", WorkspaceRoot: root, CWD: root, WorkspaceRoots: []string{root}, MemoryStore: memory.NewStore(t.TempDir())})
	if err == nil {
		t.Fatal("expected project root outside workspace roots to fail")
	}
}

func TestBuildSkipsOptionalCollectors(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), "project instruction")
	writeSkillFile(t, root, ".agents", "release", "release", "Release workflow", "body")
	store := memory.NewStore(t.TempDir())
	if _, err := store.Upsert("ws_test", "general", "general", "memory note"); err != nil {
		t.Fatal(err)
	}
	value, err := Build(context.Background(), BuildOptions{
		Root: root, WorkspaceID: "ws_test", WorkspaceRoot: root, CWD: root, WorkspaceRoots: []string{root}, MemoryStore: store,
		SkipGit: true, SkipMemory: true, SkipSkills: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !value.Git.Skipped || value.Git.IsRepo || len(value.ProjectMemory.Sections) != 0 || value.AutoMemory.Loaded || len(value.Skills) != 0 {
		t.Fatalf("value = %#v", value)
	}
	for _, unexpected := range []string{"## Git", "## Auto memory", "## Project instructions", "## User instructions", "## Skills", "project instruction", "memory note", "Release workflow"} {
		if strings.Contains(value.InstructionsText, unexpected) {
			t.Fatalf("skipped content %q rendered:\n%s", unexpected, value.InstructionsText)
		}
	}
}

func TestBuildLimitsFinalInstructions(t *testing.T) {
	root := t.TempDir()
	writeInstructionFile(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("instruction ", 500))
	value, err := Build(context.Background(), BuildOptions{
		Root: root, WorkspaceID: "ws_test", WorkspaceRoot: root, CWD: root, WorkspaceRoots: []string{root}, MemoryStore: memory.NewStore(t.TempDir()), MaxInstructionBytes: 6656,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !value.InstructionTruncated || value.InstructionBytes > 6656 || value.InstructionBytes != len([]byte(value.InstructionsText)) {
		t.Fatalf("value = %#v", value)
	}
	for _, required := range []string{"## Agent workflow", "## Tool profile", "## Environment", "## Quick pointers"} {
		if !strings.Contains(value.InstructionsText, required) {
			t.Fatalf("required block %q was evicted:\n%s", required, value.InstructionsText)
		}
	}
	if strings.Contains(value.InstructionsText, strings.Repeat("instruction ", 100)) {
		t.Fatalf("oversized optional project instructions were partially rendered:\n%s", value.InstructionsText)
	}
}

func TestBuildRejectsBudgetBelowMandatoryContext(t *testing.T) {
	root := t.TempDir()
	_, err := Build(context.Background(), BuildOptions{
		Root: root, WorkspaceID: "ws_test", WorkspaceRoot: root, CWD: root, WorkspaceRoots: []string{root},
		MemoryStore: memory.NewStore(t.TempDir()), MaxInstructionBytes: 512, SkipGit: true, SkipMemory: true, SkipSkills: true,
	})
	if err == nil || !strings.Contains(err.Error(), "required project context minimum") {
		t.Fatalf("budget error=%v", err)
	}
}
