package application

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/memory"
	plandoc "go.mewis.me/codemcp/internal/plan"
	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
	statepkg "go.mewis.me/codemcp/internal/state"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestWorkspaceMergeReconcilesTypedStateAndDropsTransientState(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "workspaces.json")
	manager := workspace.NewManager(storePath)
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}
	sourceLocal := workspacestate.New(source)
	destinationLocal := workspacestate.New(destination)

	if err := writeMemoryFixture(sourceLocal.MemoryRoot(), memory.Document{Entries: []memory.Entry{{Scope: "project", Key: "source", Note: "left"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writeMemoryFixture(destinationLocal.MemoryRoot(), memory.Document{Entries: []memory.Entry{{Scope: "project", Key: "destination", Note: "right"}}}); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceMergeFile(t, filepath.Join(sourceLocal.RulesRoot(), "source.md"), "source rule")
	writeWorkspaceMergeFile(t, filepath.Join(destinationLocal.RulesRoot(), "destination.mdc"), "destination rule")
	writeWorkspaceMergeFile(t, filepath.Join(sourceLocal.SkillsRoot(), "source", "SKILL.md"), "source skill")
	writeWorkspaceMergeFile(t, filepath.Join(destinationLocal.SkillsRoot(), "destination", "SKILL.md"), "destination skill")
	writeWorkspaceMergeFile(t, filepath.Join(sourceLocal.PromptRoot(), "source.json"), `{"version":1,"name":"source","messages":[{"role":"user","content":{"type":"text","text":"left"}}]}`)
	writeWorkspaceMergeFile(t, filepath.Join(destinationLocal.PromptRoot(), "destination.json"), `{"version":1,"name":"destination","messages":[{"role":"user","content":{"type":"text","text":"right"}}]}`)
	writeWorkspaceMergeBytes(t, filepath.Join(sourceLocal.PlansRoot(), "source-plan.md"), workspacePlanFixture(t, "Source plan", false))
	writeWorkspaceMergeBytes(t, filepath.Join(destinationLocal.PlansRoot(), "destination-plan.md"), workspacePlanFixture(t, "Destination plan", false))

	if err := os.MkdirAll(filepath.Join(source, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(destination, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	sourceShell, _ := sourceLocal.StatePath("shell.json")
	destinationShell, _ := destinationLocal.StatePath("shell.json")
	if err := statepkg.WriteJSONAtomic(sourceShell, shellruntime.SessionState{
		Version: 1, WorkspaceID: registered.Value.ID, CWD: filepath.Join(source, "sub"), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		RecentCommands: []string{"source-newer"},
	}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := statepkg.WriteJSONAtomic(destinationShell, shellruntime.SessionState{
		Version: 1, WorkspaceID: registered.Value.ID, CWD: destination, UpdatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
		RecentCommands: []string{"destination-older"},
	}, 0600); err != nil {
		t.Fatal(err)
	}
	sourceCodegraph, _ := sourceLocal.StatePath("codegraph.json")
	destinationCodegraph, _ := destinationLocal.StatePath("codegraph.json")
	writeWorkspaceMergeFile(t, sourceCodegraph, `{"derived":"source"}`)
	writeWorkspaceMergeFile(t, destinationCodegraph, `{"derived":"destination"}`)
	writeWorkspaceMergeFile(t, filepath.Join(sourceLocal.CacheRoot(), "source.cache"), "source cache")
	writeWorkspaceMergeFile(t, filepath.Join(destinationLocal.CacheRoot(), "destination.cache"), "destination cache")

	result, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.After.ID != registered.Value.ID || result.Value.After.Path != filepath.Clean(destination) {
		t.Fatalf("relocation=%#v", result.Value)
	}
	if _, err := os.Stat(sourceLocal.IdentityPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source local state remains authoritative: %v", err)
	}
	if identity, err := destinationLocal.LoadIdentity(); err != nil || identity.ID != registered.Value.ID {
		t.Fatalf("destination identity=%#v err=%v", identity, err)
	}
	if _, err := os.Stat(destinationLocal.RuntimeRoot()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime state survived merge: %v", err)
	}
	if _, err := os.Stat(destinationLocal.CacheRoot()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache state survived merge: %v", err)
	}
	if _, err := os.Stat(destinationCodegraph); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("codegraph metadata survived merge: %v", err)
	}
	for _, path := range []string{
		filepath.Join(destinationLocal.RulesRoot(), "source.md"),
		filepath.Join(destinationLocal.RulesRoot(), "destination.mdc"),
		filepath.Join(destinationLocal.SkillsRoot(), "source", "SKILL.md"),
		filepath.Join(destinationLocal.SkillsRoot(), "destination", "SKILL.md"),
		filepath.Join(destinationLocal.PromptRoot(), "source.json"),
		filepath.Join(destinationLocal.PromptRoot(), "destination.json"),
		filepath.Join(destinationLocal.PlansRoot(), "source-plan.md"),
		filepath.Join(destinationLocal.PlansRoot(), "destination-plan.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("merged durable path missing %s: %v", path, err)
		}
	}
	memoryData, err := os.ReadFile(filepath.Join(destinationLocal.MemoryRoot(), "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if document := memory.Parse(string(memoryData)); len(document.Entries) != 2 {
		t.Fatalf("memory=%#v", document)
	}
	mergedShell, err := os.ReadFile(destinationShell)
	if err != nil {
		t.Fatal(err)
	}
	var shell shellruntime.SessionState
	if err := json.Unmarshal(mergedShell, &shell); err != nil {
		t.Fatal(err)
	}
	if shell.CWD != filepath.Join(destination, "sub") || len(shell.RecentCommands) != 1 || shell.RecentCommands[0] != "source-newer" {
		t.Fatalf("shell=%#v", shell)
	}

	reloaded := workspace.NewManager(storePath)
	if got, err := reloaded.Get(registered.Value.ID); err != nil || got.Path != filepath.Clean(destination) {
		t.Fatalf("reloaded=%#v err=%v", got, err)
	}
	if err := reloaded.Activate(); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Deactivate(); err != nil {
		t.Fatal(err)
	}
	if again, err := manager.Register(destination); err != nil || again.ID != registered.Value.ID {
		t.Fatalf("idempotent register=%#v err=%v", again, err)
	}
	if again, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	}); err != nil || again.Value.After.ID != registered.Value.ID {
		t.Fatalf("idempotent relocate=%#v err=%v", again.Value, err)
	}
}

func TestWorkspaceMergeAcceptsCanonicalEquivalentPlanRevisions(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(workspacestate.New(source).PlansRoot(), "shared.md")
	destinationPath := filepath.Join(workspacestate.New(destination).PlansRoot(), "shared.md")
	canonical := workspacePlanFixture(t, "Shared plan", false)
	writeWorkspaceMergeBytes(t, sourcePath, canonical)
	writeWorkspaceMergeBytes(t, destinationPath, []byte(strings.ReplaceAll(string(canonical), "\n", "\r\n")))

	result, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value.After.ID != registered.Value.ID || result.Value.After.Path != filepath.Clean(destination) {
		t.Fatalf("relocation=%#v", result.Value)
	}
	merged, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := plandoc.Parse(merged)
	if err != nil {
		t.Fatal(err)
	}
	want, err := plandoc.Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if document.ContentID() != want.ContentID() {
		t.Fatalf("merged plan content id=%q want=%q", document.ContentID(), want.ContentID())
	}
}

func TestWorkspaceMergeRejectsDivergentPlanStateBeforeMutation(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(workspacestate.New(source).PlansRoot(), "shared.md")
	destinationPath := filepath.Join(workspacestate.New(destination).PlansRoot(), "shared.md")
	sourceData := workspacePlanFixture(t, "Source plan", false)
	destinationData := workspacePlanFixture(t, "Destination plan", false)
	writeWorkspaceMergeBytes(t, sourcePath, sourceData)
	writeWorkspaceMergeBytes(t, destinationPath, destinationData)

	_, err = service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if ErrorCodeOf(err) != ErrorConflict {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
	if data, readErr := os.ReadFile(sourcePath); readErr != nil || string(data) != string(sourceData) {
		t.Fatalf("source plan mutated: err=%v data=%q", readErr, data)
	}
	if data, readErr := os.ReadFile(destinationPath); readErr != nil || string(data) != string(destinationData) {
		t.Fatalf("destination plan mutated: err=%v data=%q", readErr, data)
	}
	if got, getErr := manager.Get(registered.Value.ID); getErr != nil || got.Path != filepath.Clean(source) {
		t.Fatalf("registry mutated: %#v err=%v", got, getErr)
	}
}

func TestWorkspacePlanStateFollowsRelocationAndPurgeBoundaries(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}

	planData := workspacePlanFixture(t, "Relocated plan", false)
	sourcePlan := filepath.Join(workspacestate.New(source).PlansRoot(), "relocated.md")
	writeWorkspaceMergeBytes(t, sourcePlan, planData)
	before, err := plandoc.Parse(planData)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, destination); err != nil {
		t.Fatal(err)
	}

	relocated, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{ID: registered.Value.ID, Path: destination})
	if err != nil {
		t.Fatal(err)
	}
	if relocated.Value.After.ID != registered.Value.ID || relocated.Value.After.Path != filepath.Clean(destination) {
		t.Fatalf("relocation=%#v", relocated.Value)
	}
	destinationPlan := filepath.Join(workspacestate.New(destination).PlansRoot(), "relocated.md")
	afterData, err := os.ReadFile(destinationPlan)
	if err != nil {
		t.Fatal(err)
	}
	after, err := plandoc.Parse(afterData)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterData) != string(planData) || after.ContentID() != before.ContentID() {
		t.Fatalf("relocated plan changed: id=%q want=%q", after.ContentID(), before.ContentID())
	}

	if _, err := service.Purge(t.Context(), registered.Value.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspacestate.New(destination).Root()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace local state survived purge: %v", err)
	}
}

func TestWorkspaceMergeDoesNotConsumeRepositoryLocalPlanningArtifacts(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}
	sourcePrivate := filepath.Join(source, ".local", "plans", "private.md")
	destinationPrivate := filepath.Join(destination, ".local", "plans", "private.md")
	writeWorkspaceMergeFile(t, sourcePrivate, "source-private")
	writeWorkspaceMergeFile(t, destinationPrivate, "destination-private")

	if _, err := service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sourcePrivate); err != nil || string(data) != "source-private" {
		t.Fatalf("source private planning artifact changed: err=%v data=%q", err, data)
	}
	if data, err := os.ReadFile(destinationPrivate); err != nil || string(data) != "destination-private" {
		t.Fatalf("destination private planning artifact changed: err=%v data=%q", err, data)
	}
}

func TestWorkspaceMergeRejectsDivergentDurableStateBeforeMutation(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}
	sourceRule := filepath.Join(workspacestate.New(source).RulesRoot(), "shared.md")
	destinationRule := filepath.Join(workspacestate.New(destination).RulesRoot(), "shared.md")
	writeWorkspaceMergeFile(t, sourceRule, "left")
	writeWorkspaceMergeFile(t, destinationRule, "right")

	_, err = service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if ErrorCodeOf(err) != ErrorConflict {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
	if data, _ := os.ReadFile(sourceRule); string(data) != "left" {
		t.Fatalf("source mutated: %q", data)
	}
	if data, _ := os.ReadFile(destinationRule); string(data) != "right" {
		t.Fatalf("destination mutated: %q", data)
	}
	if got, getErr := manager.Get(registered.Value.ID); getErr != nil || got.Path != filepath.Clean(source) {
		t.Fatalf("registry mutated: %#v err=%v", got, getErr)
	}
}

func TestWorkspaceMergeRejectsUnknownStateBeforeMutation(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	service := NewWorkspaceService(manager, nil)
	source := t.TempDir()
	registered, err := service.Register(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if _, _, err := workspacestate.New(destination).EnsureIdentity(registered.Value.ID); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(workspacestate.New(destination).Root(), "future-state.bin")
	writeWorkspaceMergeFile(t, unknown, "unknown")

	_, err = service.Relocate(t.Context(), WorkspaceRelocateRequest{
		ID: registered.Value.ID, Path: destination, Resolution: workspace.RelocationResolutionMerge,
	})
	if ErrorCodeOf(err) != ErrorConflict {
		t.Fatalf("err=%v code=%s", err, ErrorCodeOf(err))
	}
	if _, statErr := os.Stat(unknown); statErr != nil {
		t.Fatalf("unknown state mutated: %v", statErr)
	}
	if _, statErr := os.Stat(workspacestate.New(source).IdentityPath()); statErr != nil {
		t.Fatalf("source identity mutated: %v", statErr)
	}
}

func writeMemoryFixture(root string, document memory.Document) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(memory.Render(document)), 0600)
}

func writeWorkspaceMergeFile(t *testing.T, path, content string) {
	t.Helper()
	writeWorkspaceMergeBytes(t, path, []byte(content))
}

func writeWorkspaceMergeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}

func workspacePlanFixture(t *testing.T, title string, completed bool) []byte {
	t.Helper()
	mark := " "
	if completed {
		mark = "x"
	}
	document, err := plandoc.ParseParts(
		"# "+title+"\n\n## Goal\nExercise workspace-owned plan state.\n\n## Phase 1A - Persist state\n\n- ["+mark+"] Persist the state.\n\n## Acceptance\nState remains durable.",
		"## Execution rules\nComplete the phase.\n\n## Why this order\nPersistence is required first.\n\n## Ordered phases\n\n- ["+mark+"] Phase 1A - Persist state\n\n## Terminal acceptance\n\n- ["+mark+"] Durable-state validation passes.",
	)
	if err != nil {
		t.Fatal(err)
	}
	return document.Render()
}
