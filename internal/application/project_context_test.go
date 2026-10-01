package application

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/instructioncontext"
	"go.mewis.me/codemcp/internal/projectcontext"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestApplicationProjectContextMapsExactPlanMissToNotFound(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	item, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewApplicationProjectContextService(manager).Read(t.Context(), ProjectContextInput{
		WorkspaceID: item.ID,
		Options:     projectcontext.Options{PlanName: "missing-plan"},
	})
	if err == nil || !errors.Is(err, projectcontext.ErrPlanNotFound) {
		t.Fatalf("project context missing plan error=%v", err)
	}
	semantics := ErrorSemanticsOf(err)
	if semantics.Code != ErrorNotFound || semantics.Retryable || semantics.Stale {
		t.Fatalf("project context missing plan semantics=%#v", semantics)
	}
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Operation != "project.context.read" {
		t.Fatalf("project context operation error=%#v", operationErr)
	}
}

func TestApplicationProjectContextUsesCodeGraphProjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture")
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
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Integrations.CodeGraph.Enabled = true
	cfg.Integrations.CodeGraph.Path = executable
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	result, err := NewProjectContextService(t.Context(), manager).Build(t.Context(), item.ID, projectcontext.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	instruction, ok := findIntegrationInstruction(result.InstructionContext.IntegrationInstructions, "CodeGraph")
	if !ok {
		t.Fatalf("application project context missing CodeGraph guidance: %#v", result.InstructionContext.IntegrationInstructions)
	}
	if instruction.Source != "cm integration:codegraph" {
		t.Fatalf("CodeGraph guidance source=%q", instruction.Source)
	}
	if len(result.InstructionContext.IntegrationDiagnostics) != 1 || result.InstructionContext.IntegrationDiagnostics[0].State != "stale" {
		t.Fatalf("CodeGraph diagnostics=%#v", result.InstructionContext.IntegrationDiagnostics)
	}

	cfg.Integrations.CodeGraph.Enabled = false
	cfg.Integrations.CodeGraph.Path = ""
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	disabled, err := NewProjectContextService(t.Context(), manager).Build(t.Context(), item.ID, projectcontext.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findIntegrationInstruction(disabled.InstructionContext.IntegrationInstructions, "CodeGraph"); ok {
		t.Fatalf("disabled CodeGraph advertised guidance: %#v", disabled.InstructionContext.IntegrationInstructions)
	}
	for _, diagnostic := range disabled.InstructionContext.IntegrationDiagnostics {
		if diagnostic.ID == "CodeGraph" {
			t.Fatalf("disabled CodeGraph advertised diagnostic: %#v", diagnostic)
		}
	}
}

func findIntegrationInstruction(values []instructioncontext.IntegrationInstruction, id string) (instructioncontext.IntegrationInstruction, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return instructioncontext.IntegrationInstruction{}, false
}
