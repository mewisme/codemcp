package mcp

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tools"
)

func TestSDKCompletionUsesCanonicalAuthorization(t *testing.T) {
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

	adapter, err := NewSDKServerWithProfile(runtime, "completion-sdk-test", "", first.ID, BaseProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- adapter.Server.Run(ctx, serverTransport) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "completion-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil || initialized.Capabilities.Completions == nil ||
		initialized.Capabilities.Resources == nil || !initialized.Capabilities.Resources.Subscribe {
		t.Fatalf("SDK capabilities=%#v", initialized)
	}

	result, err := session.Complete(ctx, &sdkmcp.CompleteParams{
		Ref: &sdkmcp.CompleteReference{
			Type: "ref/resource",
			URI:  "cm://workspace/{workspace_id}/project-context",
		},
		Argument: sdkmcp.CompleteParamsArgument{Name: "workspace_id", Value: "ws_"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.Completion.Values) != 1 || result.Completion.Values[0] != first.ID {
		t.Fatalf("SDK completion=%#v", result)
	}
	if containsCompletion(result.Completion.Values, second.ID) {
		t.Fatalf("SDK completion leaked unbound workspace=%#v", result.Completion.Values)
	}

	_ = session.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SDK server did not stop")
	}
}

func TestSDKRejectsDeprecatedDirectResourceSubscriptionMethods(t *testing.T) {
	called := false
	middleware := rejectDeprecatedResourceSubscriptionMiddleware()
	handler := middleware(func(context.Context, string, sdkmcp.Request) (sdkmcp.Result, error) {
		called = true
		return nil, nil
	})
	for _, method := range []string{"resources/subscribe", "resources/unsubscribe"} {
		if _, err := handler(context.Background(), method, nil); err == nil {
			t.Fatalf("%s was accepted", method)
		}
	}
	if called {
		t.Fatal("deprecated resource subscription reached the next SDK handler")
	}
}

func TestSDKResourceListChangeProjectsRegistryStateBeforeNotification(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	runtime := tools.NewRuntime()
	adapter, err := NewSDKServerWithProfile(runtime, "resource-list-sdk-test", "", "", BaseProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- adapter.Server.Run(ctx, serverTransport) }()

	changed := make(chan struct{}, 1)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "resource-list-client", Version: "1.0.0"}, &sdkmcp.ClientOptions{
		ResourceListChangedHandler: func(context.Context, *sdkmcp.ResourceListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	uri, err := GlobalResourceURI("sdk-dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.FeatureRegistry.Register(FeatureRegistration{
		ID: "sdk-dynamic-resource", Family: FeatureResources,
		Resources: []ResourceDescriptor{{
			URI: uri, Name: "sdk-dynamic", MIMEType: "text/plain",
			Policy: ResourcePolicy{Cache: ResourceCachePolicy{Scope: ResourceCacheScopePrivate}},
		}},
		ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
			return TextResourceContent("dynamic"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changed:
	case <-ctx.Done():
		t.Fatal("SDK resource list change was not delivered")
	}
	listed, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, resource := range listed.Resources {
		if resource.URI == uri {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("resource list changed before SDK projection caught up: %#v", listed.Resources)
	}

	_ = session.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SDK server did not stop")
	}
}
