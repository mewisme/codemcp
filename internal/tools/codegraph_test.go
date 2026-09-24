package tools

import (
	"context"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/integrations/codegraph"
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
