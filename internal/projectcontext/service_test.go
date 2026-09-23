package projectcontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/memory"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestServiceBuildUsesManagedPolicyAndSelectedSubproject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	sub := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("subproject instruction"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("disabled user context"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	policy := instructionpolicy.DefaultConfig()
	policy.Context = "managed context"
	policy.Sources["claude"] = instructionpolicy.SourcePolicy{Context: &disabled}
	policyStore := &instructionpolicy.Store{Path: filepath.Join(t.TempDir(), "global.json")}
	if err := policyStore.Save(policy); err != nil {
		t.Fatal(err)
	}
	service := New(manager, func() instructioncontext.ToolProfile { return instructioncontext.ToolProfile{Name: "full", Count: 77} })
	service.MemoryStore = memory.NewStore(t.TempDir())
	service.PolicyStore = policyStore
	service.Environment = func() (bool, int) { return true, 37422 }
	result, err := service.Build(context.Background(), item.ID, Options{Path: "packages/app", IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	resultRootInfo, err := os.Stat(result.Root)
	if err != nil {
		t.Fatal(err)
	}
	subInfo, err := os.Stat(sub)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(resultRootInfo, subInfo) || result.WorkspaceID != item.ID || result.InstructionContext.ToolProfile.Count != 77 || !result.InstructionContext.Environment.Admin.Enabled || result.InstructionContext.Environment.Admin.URL != "http://127.0.0.1:37422/" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.InstructionContext.InstructionsText, "managed context") || !strings.Contains(result.InstructionContext.InstructionsText, "subproject instruction") || strings.Contains(result.InstructionContext.InstructionsText, "disabled user context") {
		t.Fatalf("instructions = %q", result.InstructionContext.InstructionsText)
	}
}

func TestServiceBuildRejectsFilePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())
	service.PolicyStore = &instructionpolicy.Store{Path: filepath.Join(t.TempDir(), "missing.json")}
	if _, err := service.Build(context.Background(), item.ID, Options{Path: "file.txt", IncludeMemory: true, IncludeSkills: true}); err == nil {
		t.Fatal("expected file path to fail")
	}
}

func TestDefaultOptionsMatchInstructionContextDefaults(t *testing.T) {
	options := DefaultOptions()
	if options.MaxMemoryEntries != DefaultMemoryEntries || options.MaxMemoryBytes != DefaultMemoryBytes || options.MaxInstructionBytes != instructioncontext.DefaultInstructionMaxBytes || options.MaxSectionBytes != instructioncontext.DefaultSectionMaxBytes || options.MaxLinesPerSection != instructioncontext.DefaultSectionMaxLines {
		t.Fatalf("defaults=%#v", options)
	}
	if !options.IncludeGit || !options.IncludeMemory || !options.IncludeSkills {
		t.Fatalf("collector defaults=%#v", options)
	}
}
