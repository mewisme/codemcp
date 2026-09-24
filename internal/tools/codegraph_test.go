package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/projectcontext"
)

func TestCodeGraphExploreToolRegistrationIsStableAcrossIntegrationReload(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := integrations.Default()
	cfg.CodeGraph.Enabled = false
	runtime := NewRuntimeWithIntegrations(cfg)

	schema, ok := runtime.Registry.Schema(codegraph.ToolName)
	if !ok {
		t.Fatal("codegraph_explore is not registered while CodeGraph is disabled")
	}
	scoped, err := runtime.Registry.WorkspaceScoped(codegraph.ToolName)
	if err != nil {
		t.Fatal(err)
	}
	if !scoped || schema.Name != codegraph.ToolName {
		t.Fatalf("schema=%#v scoped=%t", schema, scoped)
	}
	if got, ok := capability.ForMCPTool(codegraph.ToolName); !ok || got != capability.IntegrationCodeGraphExplore {
		t.Fatalf("capability=%q ok=%t", got, ok)
	}

	cfg.CodeGraph.Enabled = true
	if err := runtime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Registry.Schema(codegraph.ToolName); !ok {
		t.Fatal("codegraph_explore disappeared after enable")
	}
	cfg.CodeGraph.Enabled = false
	if err := runtime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Registry.Schema(codegraph.ToolName); !ok {
		t.Fatal("codegraph_explore disappeared after disable")
	}
}

func TestCodeGraphExploreDisabledResultIsTransportIndependent(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := integrations.Default()
	cfg.CodeGraph.Enabled = false
	runtime := NewRuntimeWithIntegrations(cfg)
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var first codegraph.ToolState
	for index, source := range []string{"http", "tunnel"} {
		ctx := WithCallSource(context.Background(), source)
		result, err := runtime.Call(ctx, codegraph.ToolName, map[string]any{
			"workspace_id": item.ID,
			"query":        "find Foo",
		})
		if err != nil || result.IsError {
			t.Fatalf("%s result=%#v err=%v", source, result, err)
		}
		state, ok := result.StructuredContent.(codegraph.ToolState)
		if !ok {
			t.Fatalf("%s structured=%T %#v", source, result.StructuredContent, result.StructuredContent)
		}
		if state.State != codegraph.StateDisabled || state.WorkspaceID != item.ID || state.Guidance == "" {
			t.Fatalf("%s state=%#v", source, state)
		}
		if index == 0 {
			first = state
		} else if state != first {
			t.Fatalf("transport changed state: http=%#v tunnel=%#v", first, state)
		}
	}
}

func TestCodeGraphExploreHonorsBoundWorkspaceAuthorization(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntimeWithIntegrations(integrations.Default())
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBoundWorkspace(context.Background(), first.ID)

	result, err := runtime.Call(ctx, codegraph.ToolName, map[string]any{
		"workspace_id": second.ID,
		"query":        "find Foo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "cannot access workspace") {
		t.Fatalf("bound mismatch result=%#v", result)
	}

	result, err = runtime.Call(ctx, codegraph.ToolName, map[string]any{
		"workspace_id": first.ID,
		"query":        "find Foo",
		"path":         "../outside",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) == 0 || !strings.Contains(strings.ToLower(result.Content[0].Text), "escapes workspace root") {
		t.Fatalf("path escape result=%#v", result)
	}
}

func TestCodeGraphProjectContextGuidanceTracksRuntimeReload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture")
	}
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cfg := integrations.Default()
	cfg.CodeGraph.Enabled = false
	toolRuntime := NewRuntimeWithIntegrations(cfg)
	item, err := toolRuntime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	build := func() projectcontext.Result {
		result, err := toolRuntime.Call(context.Background(), "project_context", map[string]any{"workspace_id": item.ID})
		if err != nil || result.IsError {
			t.Fatalf("project_context result=%#v err=%v", result, err)
		}
		value, ok := result.StructuredContent.(projectcontext.Result)
		if !ok {
			t.Fatalf("project_context structured=%T %#v", result.StructuredContent, result.StructuredContent)
		}
		return value
	}
	hasCodeGraphGuidance := func(value projectcontext.Result) bool {
		for _, instruction := range value.InstructionContext.IntegrationInstructions {
			if instruction.ID == "CodeGraph" {
				return true
			}
		}
		return false
	}

	if value := build(); hasCodeGraphGuidance(value) {
		t.Fatalf("disabled CodeGraph advertised guidance: %#v", value.InstructionContext.IntegrationInstructions)
	}

	executable := filepath.Join(t.TempDir(), "codegraph")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(item.Path, ".codegraph"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg.CodeGraph.Enabled = true
	cfg.CodeGraph.Path = executable
	if err := toolRuntime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	enabled := build()
	if !hasCodeGraphGuidance(enabled) {
		t.Fatalf("enabled indexed CodeGraph missing guidance: %#v", enabled.InstructionContext)
	}
	if len(enabled.InstructionContext.IntegrationDiagnostics) != 1 || enabled.InstructionContext.IntegrationDiagnostics[0].State != "stale" {
		t.Fatalf("expected stale diagnostic, got %#v", enabled.InstructionContext.IntegrationDiagnostics)
	}
	if _, ok := toolRuntime.Registry.Schema(codegraph.ToolName); !ok {
		t.Fatal("codegraph_explore disappeared while guidance was active")
	}

	cfg.CodeGraph.Enabled = false
	cfg.CodeGraph.Path = ""
	if err := toolRuntime.SyncIntegrations(cfg); err != nil {
		t.Fatal(err)
	}
	disabled := build()
	if hasCodeGraphGuidance(disabled) {
		t.Fatalf("disabled CodeGraph retained guidance after reload: %#v", disabled.InstructionContext.IntegrationInstructions)
	}
	if len(disabled.InstructionContext.IntegrationDiagnostics) != 0 {
		t.Fatalf("disabled CodeGraph retained diagnostics after reload: %#v", disabled.InstructionContext.IntegrationDiagnostics)
	}
	if _, ok := toolRuntime.Registry.Schema(codegraph.ToolName); !ok {
		t.Fatal("stable codegraph_explore tool disappeared after disable")
	}
}

func TestCodeGraphExploreBoundWorkspaceCanBeInjected(t *testing.T) {
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	runtime := NewRuntimeWithIntegrations(integrations.Default())
	item, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBoundWorkspace(context.Background(), item.ID)
	result, err := runtime.Call(ctx, codegraph.ToolName, map[string]any{"query": "find Foo"})
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	state, ok := result.StructuredContent.(codegraph.ToolState)
	if !ok || state.WorkspaceID != item.ID || state.State != codegraph.StateDisabled {
		t.Fatalf("state=%#v type=%T", result.StructuredContent, result.StructuredContent)
	}
}
