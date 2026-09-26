package mcp

import (
	"context"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestCompletionEnumeratesOnlyBoundOrPreviouslyGrantedWorkspaces(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := FeatureRegistryForRuntime(runtime)

	bound := NewFeatureExecutor(registry, runtime, first.ID, "test")
	result, err := bound.Complete(context.Background(), CompletionRequest{
		Ref:      CompletionReference{Type: "ref/resource", URI: "cm://workspace/{workspace_id}/project-context"},
		Argument: CompletionArgument{Name: "workspace_id", Value: "ws_"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Values) != 1 || result.Values[0] != first.ID {
		t.Fatalf("bound workspace completion=%#v", result)
	}

	resources, err := bound.Complete(context.Background(), CompletionRequest{
		Ref:      CompletionReference{Type: "ref/resource", URI: "cm://workspace/{workspace_id}/project-context"},
		Argument: CompletionArgument{Name: "uri", Value: "cm://"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsCompletion(resources.Values, "cm://workspace/"+first.ID+"/project-context") {
		t.Fatalf("bound resource completion=%#v", resources)
	}
	for _, value := range resources.Values {
		if strings.Contains(value, second.ID) {
			t.Fatalf("bound completion leaked second workspace: %#v", resources.Values)
		}
	}

	unbound := NewFeatureExecutor(registry, runtime, "", "test")
	noSession, err := unbound.Complete(context.Background(), CompletionRequest{
		Ref:      CompletionReference{Type: "ref/resource", URI: "cm://workspace/{workspace_id}/project-context"},
		Argument: CompletionArgument{Name: "workspace_id", Value: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(noSession.Values) != 0 {
		t.Fatalf("unbound completion enumerated registry workspaces=%#v", noSession)
	}

	ctx := tools.WithMCPSessionID(context.Background(), "completion-session")
	if _, err := runtime.ResolveWorkspaceAccess(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	granted, err := unbound.Complete(ctx, CompletionRequest{
		Ref:      CompletionReference{Type: "ref/resource", URI: "cm://workspace/{workspace_id}/project-context"},
		Argument: CompletionArgument{Name: "workspace_id", Value: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(granted.Values) != 1 || granted.Values[0] != first.ID {
		t.Fatalf("session completion=%#v", granted)
	}
	if containsCompletion(granted.Values, second.ID) {
		t.Fatalf("session completion leaked ungranted workspace=%#v", granted)
	}

	uriResult, err := unbound.Complete(ctx, CompletionRequest{
		Ref:       CompletionReference{Type: "ref/resource", URI: "cm://workspace/{workspace_id}/project-context"},
		Argument:  CompletionArgument{Name: "uri", Value: "cm://workspace/"},
		Arguments: map[string]string{"workspace_id": second.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(uriResult.Values) != 0 {
		t.Fatalf("completion context promoted an ungranted workspace=%#v", uriResult)
	}
}

func TestCompletionIsBoundedAndDirectRuntimeUsesCanonicalHandler(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	toolsRuntime := tools.NewRuntime()
	registry := FeatureRegistryForRuntime(toolsRuntime)
	for i := 0; i < maxCompletionValues+12; i++ {
		path := "completion-test/" + strings.Repeat("x", i/26+1) + string(rune('a'+i%26))
		uri, err := GlobalResourceURI(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := registry.Register(FeatureRegistration{
			ID:     "completion-test-" + string(rune(0x1000+i)),
			Family: FeatureResources,
			Resources: []ResourceDescriptor{{
				URI: uri, Name: path, MIMEType: "text/plain",
				Policy: ResourcePolicy{Cache: ResourceCachePolicy{Scope: ResourceCacheScopePrivate}},
			}},
			ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
				return TextResourceContent("ok"), nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := NewRuntimeWithTools(toolsRuntime)
	params := map[string]any{
		"ref":      map[string]any{"type": "ref/resource", "uri": "cm://global/{resource}"},
		"argument": map[string]any{"name": "uri", "value": "cm://"},
	}
	resultValue, err := server.Handle(context.Background(), CompletionCompleteMethod, params)
	if err != nil {
		t.Fatal(err)
	}
	result := resultValue.(map[string]any)
	completion := result["completion"].(map[string]any)
	values := completion["values"].([]string)
	if len(values) != maxCompletionValues || completion["hasMore"] != true {
		t.Fatalf("bounded completion=%#v", completion)
	}
	total, ok := completion["total"].(int)
	if !ok || total <= maxCompletionValues {
		t.Fatalf("completion total=%#v", completion["total"])
	}
	if !server.SupportsMethod(CompletionCompleteMethod) {
		t.Fatal("completion/complete is not routed by canonical runtime")
	}
}

func containsCompletion(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func newCompletionWorkspaceRuntime(t *testing.T) (*tools.Runtime, workspace.Workspace, workspace.Workspace) {
	t.Helper()
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	first, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Workspaces.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return runtime, first, second
}
