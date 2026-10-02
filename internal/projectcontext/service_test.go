package projectcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/instructionpolicy"
	"go.mewis.me/codemcp/internal/memory"
	plandoc "go.mewis.me/codemcp/internal/plan"
	"go.mewis.me/codemcp/internal/workspace"
	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

func TestServiceBuildIgnoresLegacyGlobalPolicyAndUsesSelectedSubproject(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
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
	policyStore := instructionpolicy.DefaultStore()
	if err := policyStore.Save(policy); err != nil {
		t.Fatal(err)
	}
	service := New(manager, func() instructioncontext.ToolProfile { return instructioncontext.ToolProfile{Name: "full", Count: 77} })
	service.MemoryStore = memory.NewStore(t.TempDir())
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
	if strings.Contains(result.InstructionContext.InstructionsText, "managed context") || !strings.Contains(result.InstructionContext.InstructionsText, "subproject instruction") || strings.Contains(result.InstructionContext.InstructionsText, "disabled user context") {
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

func TestServiceIncludesIntegrationInstructionsInProviderOrder(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	provider := func(id, content string) IntegrationInstructionProvider {
		return func(_ context.Context, workspaceID, projectRoot string) ([]instructioncontext.IntegrationInstruction, error) {
			if workspaceID != item.ID || projectRoot != item.Path {
				t.Fatalf("provider args workspace=%q root=%q", workspaceID, projectRoot)
			}
			return []instructioncontext.IntegrationInstruction{{ID: id, Source: "test", Content: content}}, nil
		}
	}
	service := NewService(ServiceOptions{
		Workspaces: manager,
		IntegrationProviders: []IntegrationInstructionProvider{
			provider("Alpha", "alpha guidance"),
			provider("Beta", "beta guidance"),
		},
	})
	result, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	values := result.InstructionContext.IntegrationInstructions
	if len(values) != 2 || values[0].ID != "Alpha" || values[1].ID != "Beta" {
		t.Fatalf("integration instructions=%#v", values)
	}
	text := result.InstructionContext.InstructionsText
	if !strings.Contains(text, "alpha guidance") || strings.Index(text, "beta guidance") < strings.Index(text, "alpha guidance") {
		t.Fatalf("integration order changed: %s", text)
	}
}

func TestServiceIncludesIntegrationProjectionDiagnosticsWithoutRenderingThem(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{
		Workspaces: manager,
		IntegrationProjectionProviders: []IntegrationProjectionProvider{
			func(_ context.Context, workspaceID, projectRoot string) (IntegrationProjection, error) {
				if workspaceID != item.ID || projectRoot != item.Path {
					t.Fatalf("provider args workspace=%q root=%q", workspaceID, projectRoot)
				}
				return IntegrationProjection{
					Instructions: []instructioncontext.IntegrationInstruction{{ID: "CodeGraph", Source: "test", Content: "use codegraph guidance"}},
					Diagnostics:  []instructioncontext.IntegrationDiagnostic{{ID: "CodeGraph", Source: "test", State: "stale", Message: "stale graph diagnostic"}},
				}, nil
			},
		},
	})
	result, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	if values := result.InstructionContext.IntegrationInstructions; len(values) != 1 || values[0].ID != "CodeGraph" {
		t.Fatalf("integration instructions=%#v", values)
	}
	if values := result.InstructionContext.IntegrationDiagnostics; len(values) != 1 || values[0].State != "stale" {
		t.Fatalf("integration diagnostics=%#v", values)
	}
	text := result.InstructionContext.InstructionsText
	if !strings.Contains(text, "use codegraph guidance") {
		t.Fatalf("projection guidance missing from instructions: %s", text)
	}
	if strings.Contains(text, "stale graph diagnostic") {
		t.Fatalf("structured diagnostic leaked into rendered instructions: %s", text)
	}
}

func TestServiceRejectsDuplicateIntegrationInstructionIDs(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{
		Workspaces: manager,
		IntegrationProviders: []IntegrationInstructionProvider{
			func(context.Context, string, string) ([]instructioncontext.IntegrationInstruction, error) {
				return []instructioncontext.IntegrationInstruction{{ID: "same", Content: "first"}}, nil
			},
			func(context.Context, string, string) ([]instructioncontext.IntegrationInstruction, error) {
				return []instructioncontext.IntegrationInstruction{{ID: "same", Content: "second"}}, nil
			},
		},
	})
	if _, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true}); err == nil || !strings.Contains(err.Error(), "duplicate project context integration instruction id") {
		t.Fatalf("duplicate integration error=%v", err)
	}
}

func TestServiceBuildSurfacesBoundedPlanSummariesAndInference(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	secret := "FULL_PLAN_BODY_SECRET"
	writeProjectContextPlan(t, root, "zeta-plan", false, secret)
	writeProjectContextPlan(t, root, "alpha-plan", true, "completed body")

	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())
	result, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	plans := result.Summary.Plans
	if len(plans.Summaries) != 2 || plans.Summaries[0].Name != "alpha-plan" || plans.Summaries[1].Name != "zeta-plan" {
		t.Fatalf("plan summaries=%#v", plans.Summaries)
	}
	if plans.Inferred == nil || plans.Inferred.Name != "zeta-plan" || plans.NonCompletedCount != 1 || !plans.ScanComplete {
		t.Fatalf("plan inference=%#v", plans)
	}
	if plans.Inferred.NextPhase == nil || plans.Inferred.NextPhase.ID != "1A" {
		t.Fatalf("inferred next phase=%#v", plans.Inferred)
	}
	if plans.Inferred.Path != ".cm/plans/zeta-plan.md" || plans.Inferred.ContentID == "" ||
		plans.Inferred.PhaseCount != 1 || plans.Inferred.CompletedPhaseCount != 0 {
		t.Fatalf("inferred summary=%#v", plans.Inferred)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "# Persisted plan") ||
		strings.Contains(string(encoded), "## Implementation order") {
		t.Fatalf("project context leaked full plan body: %s", encoded)
	}
}

func TestServiceBuildNeverInfersAmongMultipleUnfinishedPlans(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectContextPlan(t, root, "first-plan", false, "first")
	writeProjectContextPlan(t, root, "second-plan", false, "second")
	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())

	result, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Plans.Inferred != nil || result.Summary.Plans.NonCompletedCount != 2 {
		t.Fatalf("ambiguous plan state was inferred: %#v", result.Summary.Plans)
	}
}

func TestServiceBuildExactPlanSelectionSurvivesDefaultBounds(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxPlanScanCount+8; index++ {
		writeProjectContextPlan(t, root, fmt.Sprintf("a-%03d", index), true, "bounded")
	}
	targetDocument := writeProjectContextPlan(t, root, "z-target", false, "selected body secret")
	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())

	defaultResult, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	defaultPlans := defaultResult.Summary.Plans
	if defaultPlans.ScanComplete || defaultPlans.Inferred != nil || defaultPlans.TotalEntries != MaxPlanScanCount+9 ||
		defaultPlans.ScannedEntries != MaxPlanScanCount || len(defaultPlans.Summaries) > MaxPlanSummaryCount ||
		defaultPlans.SummaryBytes > MaxPlanSummaryBytes {
		t.Fatalf("default bounded plans=%#v", defaultPlans)
	}
	for _, summary := range defaultPlans.Summaries {
		if summary.Name == "z-target" {
			t.Fatalf("target unexpectedly fell inside default summary window: %#v", defaultPlans.Summaries)
		}
	}

	selectedResult, err := service.Build(context.Background(), item.ID, Options{
		IncludeMemory: true, IncludeSkills: true, PlanName: "z-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := selectedResult.Summary.Plans.Selected
	if selected == nil || selected.Name != "z-target" || selected.ContentID != targetDocument.ContentID() ||
		selected.NextPhase == nil || selected.NextPhase.ID != "1A" {
		t.Fatalf("selected plan=%#v", selected)
	}
	found := false
	for _, summary := range selectedResult.Summary.Plans.Summaries {
		found = found || summary.Name == "z-target"
	}
	if !found || len(selectedResult.Summary.Plans.Summaries) > MaxPlanSummaryCount ||
		selectedResult.Summary.Plans.SummaryBytes > MaxPlanSummaryBytes {
		t.Fatalf("selected summary was not bounded/included: %#v", selectedResult.Summary.Plans)
	}
}

func TestServiceBuildExactPlanSelectionReturnsTypedNotFound(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())

	_, err = service.Build(context.Background(), item.ID, Options{
		IncludeMemory: true, IncludeSkills: true, PlanName: "missing-plan",
	})
	if !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("missing plan error=%v", err)
	}
	var notFound *PlanNotFoundError
	if !errors.As(err, &notFound) || notFound.Name != "missing-plan" {
		t.Fatalf("typed missing plan error=%#v", notFound)
	}
}

func TestServiceBuildSurfacesCorruptPlanDiagnosticsWithoutDroppingValidPlans(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectContextPlan(t, root, "valid-plan", false, "valid")
	plansRoot := workspacestate.New(root).PlansRoot()
	const corruptSecret = "CORRUPT_PLAN_BODY_SECRET"
	if err := os.WriteFile(filepath.Join(plansRoot, "bad-plan.md"), []byte("# malformed "+corruptSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(manager, nil)
	service.MemoryStore = memory.NewStore(t.TempDir())

	result, err := service.Build(context.Background(), item.ID, Options{IncludeMemory: true, IncludeSkills: true})
	if err != nil {
		t.Fatal(err)
	}
	plans := result.Summary.Plans
	if plans.DiagnosticCount != 1 || len(plans.Diagnostics) != 1 || plans.Diagnostics[0].Name != "bad-plan" ||
		len([]rune(plans.Diagnostics[0].Message)) > maxPlanDiagnosticRunes+1 {
		t.Fatalf("plan diagnostics=%#v", plans)
	}
	if strings.Contains(plans.Diagnostics[0].Message, corruptSecret) {
		t.Fatalf("plan diagnostic leaked authored content: %#v", plans.Diagnostics[0])
	}
	if plans.Inferred != nil || len(plans.Summaries) != 1 || plans.Summaries[0].Name != "valid-plan" {
		t.Fatalf("corrupt state should remain visible without unsafe inference: %#v", plans)
	}
}

func writeProjectContextPlan(t *testing.T, root, name string, completed bool, bodyMarker string) plandoc.Document {
	t.Helper()
	mark := " "
	if completed {
		mark = "x"
	}
	planContent := "# Persisted plan\n\n## Goal\n" + bodyMarker +
		"\n\n## Phase 1A - Persist state\n\n- [" + mark + "] Persist the state.\n\n## Acceptance\nState is durable."
	orderContent := "## Execution rules\nComplete the phase.\n\n## Why this order\nPersistence comes first.\n\n## Ordered phases\n\n- [" +
		mark + "] Phase 1A - Persist state\n\n## Terminal acceptance\n\n- [" + mark + "] Durable-state validation passes."
	document, err := plandoc.ParseParts(planContent, orderContent)
	if err != nil {
		t.Fatal(err)
	}
	plansRoot := workspacestate.New(root).PlansRoot()
	if err := os.MkdirAll(plansRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plansRoot, name+".md"), document.Render(), 0o600); err != nil {
		t.Fatal(err)
	}
	return document
}
