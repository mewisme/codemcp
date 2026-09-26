package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
	"go.mewis.me/codemcp/internal/workspace"
)

func TestParseResourceURICannotEscapeScope(t *testing.T) {
	valid := []struct {
		uri       string
		scope     FeatureScope
		workspace string
		path      string
	}{
		{"cm://global/status", FeatureScopeGlobal, "", "status"},
		{"cm://global/instructions/current", FeatureScopeGlobal, "", "instructions/current"},
		{"cm://workspace/ws_abc/project-context", FeatureScopeWorkspace, "ws_abc", "project-context"},
	}
	for _, tc := range valid {
		got, err := ParseResourceURI(tc.uri)
		if err != nil {
			t.Fatalf("%s: %v", tc.uri, err)
		}
		if got.URI != tc.uri || got.Scope != tc.scope || got.WorkspaceID != tc.workspace || got.Path != tc.path {
			t.Fatalf("%s parsed=%#v", tc.uri, got)
		}
	}

	for _, uri := range []string{
		"",
		"http://global/status",
		"cm://unknown/status",
		"cm://global/../workspace/ws_abc/status",
		"cm://global/a/./b",
		"cm://global/a//b",
		"cm://global/status?workspace=ws_abc",
		"cm://global/status#fragment",
		"cm://user@global/status",
		"cm://workspace/ws_abc/../global/status",
		"cm://workspace/ws_abc/%2e%2e/global/status",
		"cm://workspace/ws_abc/a%2Fb",
		"cm://workspace/ws_abc\\global/status",
		"cm://workspace/wsc_abc/project-context",
		"cm://workspace/{workspace_id}/project-context",
		" cm://global/status",
		"cm://global/status ",
	} {
		if got, err := ParseResourceURI(uri); err == nil {
			t.Fatalf("ParseResourceURI(%q)=%#v, want error", uri, got)
		}
	}

	global, err := GlobalResourceURI("instructions/current")
	if err != nil || global != "cm://global/instructions/current" {
		t.Fatalf("global uri=%q err=%v", global, err)
	}
	workspaceURI, err := WorkspaceResourceURI("ws_abc", "project-context")
	if err != nil || workspaceURI != "cm://workspace/ws_abc/project-context" {
		t.Fatalf("workspace uri=%q err=%v", workspaceURI, err)
	}
	template, err := WorkspaceResourceTemplate("project-context")
	if err != nil || template != "cm://workspace/{workspace_id}/project-context" {
		t.Fatalf("workspace template=%q err=%v", template, err)
	}
}

func TestResourceRegistrationNormalizesTypedPolicyAndProfileProjection(t *testing.T) {
	reader := func(context.Context, ResourceReadRequest) (ResourceContent, error) {
		return TextResourceContent("ok"), nil
	}
	invalid := []FeatureRegistration{
		{
			ID: "workspace-exact", Family: FeatureResources,
			Resources:    []ResourceDescriptor{{URI: "cm://workspace/ws_abc/project-context", Name: "bad", MIMEType: "text/plain"}},
			ReadResource: reader,
		},
		{
			ID: "missing-mime", Family: FeatureResources,
			Resources:    []ResourceDescriptor{{URI: "cm://global/status", Name: "status"}},
			ReadResource: reader,
		},
		{
			ID: "public-workspace", Family: FeatureResources,
			ResourceTemplates: []ResourceTemplateDescriptor{{
				URITemplate: "cm://workspace/{workspace_id}/project-context", Name: "context", MIMEType: "application/json",
				Policy: ResourcePolicy{Cache: ResourceCachePolicy{Scope: ResourceCacheScopePublic}},
			}},
			ReadResource: reader,
		},
		{
			ID: "oversized", Family: FeatureResources,
			Resources: []ResourceDescriptor{{
				URI: "cm://global/status", Name: "status", MIMEType: "application/json",
				Policy: ResourcePolicy{MaxBytes: maxResourceMaxBytes + 1},
			}},
			ReadResource: reader,
		},
	}
	for _, registration := range invalid {
		if err := NewFeatureRegistry().Register(registration); err == nil {
			t.Fatalf("invalid resource registration %q was accepted", registration.ID)
		}
	}

	registry := NewFeatureRegistry()
	if err := registry.Register(FeatureRegistration{
		ID: "workspace-context", Family: FeatureResources,
		ResourceTemplates: []ResourceTemplateDescriptor{{
			URITemplate: "cm://workspace/{workspace_id}/project-context",
			Name:        "project-context",
			MIMEType:    "application/json; charset=utf-8",
			Policy: ResourcePolicy{
				Cache:        ResourceCachePolicy{TTLMs: 2500},
				Subscription: ResourceSubscriptionPolicy{Allowed: true},
			},
		}},
		ReadResource: reader,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := registry.Snapshot()
	if len(snapshot.ResourceTemplates) != 1 {
		t.Fatalf("templates=%#v", snapshot.ResourceTemplates)
	}
	got := snapshot.ResourceTemplates[0]
	if got.Policy.MaxBytes != defaultResourceMaxBytes ||
		got.Policy.Cache.Scope != ResourceCacheScopePrivate ||
		got.Policy.Cache.TTLMs != 2500 ||
		got.MIMEType != "application/json" {
		t.Fatalf("normalized template=%#v", got)
	}
	if snapshot.Capabilities.Resources == nil || snapshot.Capabilities.Resources.Subscribe ||
		!got.Policy.Subscription.Allowed {
		t.Fatalf("resource capabilities=%#v", snapshot.Capabilities.Resources)
	}

	descriptor := DescribeProtocolWithFeatures(nil, registry)
	base := ProjectFeatures(BaseProfile(), descriptor)
	openai := ProjectFeatures(OpenAIProfile(), descriptor)
	if !reflect.DeepEqual(base.ResourceTemplates, openai.ResourceTemplates) {
		t.Fatalf("profile changed resource policy: base=%#v openai=%#v", base.ResourceTemplates, openai.ResourceTemplates)
	}
	if len(base.ResourceTemplates) != 1 || base.ResourceTemplates[0].Policy != got.Policy {
		t.Fatalf("projected resource policy=%#v", base.ResourceTemplates)
	}
}

func TestResourceMethodsUseCanonicalWireShapesAndModernErrors(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), LoopGuard: tools.NewToolLoopGuard()}
	registry := FeatureRegistryForRuntime(runtime)
	if err := registry.Register(FeatureRegistration{
		ID: "wire-resources", Family: FeatureResources,
		Resources: []ResourceDescriptor{{
			URI: "cm://global/wire-status", Name: "wire-status", Title: "Status",
			Description: "Readiness", MIMEType: "application/json", Size: 2,
			Policy: ResourcePolicy{
				MaxBytes: 64,
				Cache:    ResourceCachePolicy{TTLMs: 1200, Scope: ResourceCacheScopePublic},
			},
		}},
		ResourceTemplates: []ResourceTemplateDescriptor{{
			URITemplate: "cm://workspace/{workspace_id}/wire-project-context", Name: "wire-project-context",
			MIMEType: "application/json",
			Policy: ResourcePolicy{
				MaxBytes: 64,
				Cache:    ResourceCachePolicy{Scope: ResourceCacheScopePrivate},
			},
		}},
		ReadResource: func(_ context.Context, request ResourceReadRequest) (ResourceContent, error) {
			if request.Scope == FeatureScopeGlobal {
				return ResourceContent{MIMEType: "application/json", Text: stringPointer("{}")}, nil
			}
			return ResourceContent{MIMEType: "application/json", Text: stringPointer("workspace:" + request.WorkspaceID)}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := NewRuntimeWithProfile(runtime, BaseProfile())
	if !server.SupportsMethod(ResourcesListMethod) || !server.SupportsMethod(ResourcesReadMethod) || !server.SupportsMethod(ResourceTemplatesListMethod) {
		t.Fatal("canonical resource methods are not routed by the feature registry")
	}

	listValue, err := server.Handle(context.Background(), ResourcesListMethod, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	list := listValue.(map[string]any)
	resources, ok := list["resources"].([]any)
	if !ok || len(resources) != 5 {
		t.Fatalf("resources/list=%T %#v", list["resources"], list["resources"])
	}
	var resource map[string]any
	for _, value := range resources {
		candidate, _ := value.(map[string]any)
		if candidate["uri"] == "cm://global/wire-status" {
			resource = candidate
			break
		}
	}
	if resource == nil || resource["mimeType"] != "application/json" {
		t.Fatalf("resource wire descriptor=%#v", resources)
	}
	if _, leaked := resource["policy"]; leaked {
		t.Fatalf("internal resource policy leaked into standard descriptor: %#v", resource)
	}
	if _, leaked := resource["mime_type"]; leaked {
		t.Fatalf("internal JSON field leaked into standard descriptor: %#v", resource)
	}

	templateValue, err := server.Handle(context.Background(), ResourceTemplatesListMethod, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	templateList := templateValue.(map[string]any)
	templates, ok := templateList["resourceTemplates"].([]any)
	if !ok || len(templates) != 5 {
		t.Fatalf("resources/templates/list=%T %#v", templateList["resourceTemplates"], templateList["resourceTemplates"])
	}
	var template map[string]any
	for _, value := range templates {
		candidate, _ := value.(map[string]any)
		if candidate["uriTemplate"] == "cm://workspace/{workspace_id}/wire-project-context" {
			template = candidate
			break
		}
	}
	if template == nil {
		t.Fatalf("resource template wire descriptor=%#v", templates)
	}

	readValue, err := server.Handle(context.Background(), ResourcesReadMethod, map[string]any{"uri": "cm://global/wire-status"})
	if err != nil {
		t.Fatal(err)
	}
	read := readValue.(map[string]any)
	if read["cacheScope"] != ResourceCacheScopePublic {
		t.Fatalf("resources/read cache policy=%#v", read)
	}

	_, err = server.Handle(context.Background(), ResourcesReadMethod, map[string]any{"uri": "cm://global/missing"})
	var protocol *Error
	if !errors.As(err, &protocol) || protocol.Code != ErrInvalidParams || protocol.Message != "Resource not found" {
		t.Fatalf("resource-not-found error=%#v", err)
	}
	data, ok := protocol.Data.(map[string]any)
	if !ok || data["uri"] != "cm://global/missing" {
		t.Fatalf("resource-not-found data=%#v", protocol.Data)
	}

	_, err = server.Handle(context.Background(), ResourcesReadMethod, map[string]any{"uri": "cm://global/../workspace/ws_x/status"})
	if !errors.As(err, &protocol) || protocol.Code != ErrInvalidParams {
		t.Fatalf("invalid resource URI error=%#v", err)
	}
	_, err = server.Handle(context.Background(), ResourcesListMethod, map[string]any{"cursor": 1})
	if !errors.As(err, &protocol) || protocol.Code != ErrInvalidParams {
		t.Fatalf("invalid cursor error=%#v", err)
	}
}

func TestResourceAuthorizationPrecedesResolutionAcrossProfiles(t *testing.T) {
	manager := workspace.NewManager(filepath.Join(t.TempDir(), "workspaces.json"))
	first, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Register(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &tools.Runtime{
		Registry:      tools.NewRegistry(),
		Workspaces:    manager,
		SessionAccess: tools.NewSessionWorkspaceAccessManager(),
		LoopGuard:     tools.NewToolLoopGuard(),
	}
	registry := FeatureRegistryForRuntime(runtime)
	var calls atomic.Int32
	if err := registry.Register(FeatureRegistration{
		ID: "authorized-template", Family: FeatureResources,
		ResourceTemplates: []ResourceTemplateDescriptor{{
			URITemplate: "cm://workspace/{workspace_id}/authorization-context",
			Name:        "authorization-context",
			MIMEType:    "application/json",
			Policy: ResourcePolicy{
				MaxBytes: 256,
				Cache:    ResourceCachePolicy{Scope: ResourceCacheScopePrivate},
			},
		}},
		ReadResource: func(_ context.Context, request ResourceReadRequest) (ResourceContent, error) {
			calls.Add(1)
			return ResourceContent{
				MIMEType: "application/json",
				Text:     stringPointer("workspace:" + request.WorkspaceID),
			}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	allowedURI := "cm://workspace/" + first.ID + "/authorization-context"
	deniedURI := "cm://workspace/" + second.ID + "/authorization-context"
	fallback := NewFeatureExecutor(registry, runtime, first.ID, "test").ResourceToolFallback("uri")
	beforeFallback := calls.Load()
	if _, err := fallback(context.Background(), map[string]any{"uri": deniedURI}); err == nil {
		t.Fatal("Tool fallback allowed a resource outside the bound workspace")
	}
	if calls.Load() != beforeFallback {
		t.Fatal("denied Tool fallback reached content resolver")
	}

	for name, profile := range map[string]Profile{"base": BaseProfile(), "openai": OpenAIProfile()} {
		t.Run(name, func(t *testing.T) {
			server, err := NewSDKServerWithProfile(runtime, "resource-auth-test", "", first.ID, profile)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
			done := make(chan error, 1)
			go func() { done <- server.Server.Run(ctx, serverTransport) }()

			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "resource-auth-client", Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if initialized := session.InitializeResult(); initialized == nil || initialized.Capabilities == nil || initialized.Capabilities.Resources == nil {
				t.Fatal("resource capability was not advertised")
			} else if initialized.Capabilities.Resources.Subscribe {
				t.Fatal("resource subscription was advertised before a subscription adapter exists")
			}

			templates, err := session.ListResourceTemplates(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			foundTemplate := false
			for _, value := range templates.ResourceTemplates {
				if value.URITemplate == "cm://workspace/{workspace_id}/authorization-context" {
					foundTemplate = true
					break
				}
			}
			if !foundTemplate {
				t.Fatalf("templates=%#v", templates.ResourceTemplates)
			}
			resources, err := session.ListResources(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range resources.Resources {
				if strings.Contains(value.URI, first.ID) || strings.Contains(value.URI, second.ID) {
					t.Fatalf("workspace identities leaked through resources/list: %#v", resources.Resources)
				}
			}

			beforeDenied := calls.Load()
			if result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: deniedURI}); err == nil || result != nil {
				t.Fatalf("denied workspace read result=%#v err=%v", result, err)
			}
			if calls.Load() != beforeDenied {
				t.Fatalf("denied workspace reached content resolver: before=%d after=%d", beforeDenied, calls.Load())
			}

			result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: allowedURI})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Contents) != 1 || result.Contents[0].URI != allowedURI ||
				result.CacheScope != ResourceCacheScopePrivate {
				t.Fatalf("authorized resource=%#v", result)
			}
			if _, leaked := result.Meta[resourceCacheScopeMetaKey]; leaked {
				t.Fatalf("internal cache-policy marker leaked to client metadata: %#v", result.Meta)
			}

			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("SDK server did not stop")
			}
		})
	}
}

func TestLargeResourceIsBoundedBeforeNativeAndFallbackSerialization(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), LoopGuard: tools.NewToolLoopGuard()}
	registry := FeatureRegistryForRuntime(runtime)
	var calls atomic.Int32
	if err := registry.Register(FeatureRegistration{
		ID: "bounded-resource", Family: FeatureResources,
		Resources: []ResourceDescriptor{{
			URI: "cm://global/bounded", Name: "bounded", MIMEType: "text/plain",
			Policy: ResourcePolicy{
				MaxBytes: 8,
				Cache:    ResourceCachePolicy{Scope: ResourceCacheScopePrivate},
			},
		}},
		ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
			calls.Add(1)
			return TextResourceContent("123456789"), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(registry, runtime, "", "test")
	_, nativeErr := executor.ReadResource(context.Background(), "cm://global/bounded")
	var protocol *Error
	if !errors.As(nativeErr, &protocol) || protocol.Code != ErrInternal || protocol.Message != "Resource content exceeds size limit" {
		t.Fatalf("native resource bound error=%#v", nativeErr)
	}

	runtime.Registry.MustRegister("resource_read_fallback", tools.Schema{
		Name:        "resource_read_fallback",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string"}},"required":["uri"],"additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, executor.ResourceToolFallback("uri"))
	fallback, err := runtime.Call(context.Background(), "resource_read_fallback", map[string]any{"uri": "cm://global/bounded"})
	if err != nil || !fallback.IsError || len(fallback.Content) == 0 ||
		fallback.Content[0].Text != "Resource content exceeds size limit" {
		t.Fatalf("fallback resource bound=%#v err=%v", fallback, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("native and fallback did not invoke one owner: calls=%d", calls.Load())
	}

	server, err := NewSDKServerWithProfile(runtime, "resource-bound-test", "", "", BaseProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- server.Server.Run(ctx, serverTransport) }()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "resource-bound-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if result, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "cm://global/bounded"}); err == nil || result != nil {
		t.Fatalf("oversized SDK resource escaped serialization bound: result=%#v err=%v", result, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SDK server did not stop")
	}
}

func stringPointer(value string) *string { return &value }
