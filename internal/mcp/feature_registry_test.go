package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
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

const featureProbeMethod = "io.codemcp.test/feature-probe"

type featureProbeClientParams struct {
	sdkmcp.ParamsBase
	Value string `json:"value,omitempty"`
}

type featureProbeClientResult struct {
	sdkmcp.ResultBase
	Value       string `json:"value,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

func TestFeatureRegistryIsCanonicalAcrossProfilesAndSDKServers(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry()}
	registry := FeatureRegistryForRuntime(runtime)
	if again := FeatureRegistryForRuntime(runtime); again != registry {
		t.Fatal("tool runtime created more than one protocol feature registry")
	}
	if err := registry.Register(FeatureRegistration{
		ID:     "resources-test",
		Family: FeatureResources,
		Resources: []ResourceDescriptor{{
			URI: "cm://global/test-resource", Name: "test-resource", Description: "test", MIMEType: "text/plain",
		}},
		ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
			return TextResourceContent("test"), nil
		},
		Capabilities: FeatureCapabilities{
			Resources: &ResourcesCapability{},
		},
		Methods: []FeatureMethod{{
			Name: featureProbeMethod, Scope: FeatureScopeGlobal, Custom: true,
			Handler: func(_ context.Context, request FeatureRequest) (map[string]any, error) {
				value, _ := request.Params["value"].(string)
				return map[string]any{"value": value}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(FeatureRegistration{
		ID:      "prompts-test",
		Family:  FeaturePrompts,
		Prompts: []PromptDescriptor{{Name: "test-prompt", Description: "test"}},
		Capabilities: FeatureCapabilities{
			Prompts: &PromptsCapability{ListChanged: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(FeatureRegistration{
		ID:     "skills-test",
		Family: FeatureSkills,
		Skills: []SkillDescriptor{{Name: "test-skill", Description: "test", Extensions: map[string]any{"format": "test"}}},
		Capabilities: FeatureCapabilities{
			Extensions: map[string]any{"io.codemcp.test/skills": map[string]any{"version": "1"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	descriptor := DescribeProtocolWithFeatures(nil, registry)
	base := ProjectFeatures(BaseProfile(), descriptor)
	openai := ProjectFeatures(OpenAIProfile(), descriptor)
	if !reflect.DeepEqual(base, openai) {
		t.Fatalf("profile changed canonical feature descriptors: base=%#v openai=%#v", base, openai)
	}
	if len(base.Resources) != 5 || len(base.ResourceTemplates) != 4 || len(base.Prompts) != 1 || len(base.Skills) != 4 {
		t.Fatalf("canonical feature descriptors=%#v", base)
	}
	direct := NewRuntimeWithProfile(runtime, BaseProfile())
	if direct.Features == nil || direct.Features.Registry != registry {
		t.Fatal("direct runtime created a separate feature registry")
	}
	directResult, err := direct.Handle(context.Background(), featureProbeMethod, map[string]any{"value": "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := directResult.(map[string]any); !ok || value["value"] != "direct" {
		t.Fatalf("direct feature result=%T %#v", directResult, directResult)
	}
	discoveryValue, err := direct.Handle(context.Background(), "server/discover", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	discovery, ok := discoveryValue.(DiscoverResult)
	if !ok {
		t.Fatalf("direct discovery=%T %#v", discoveryValue, discoveryValue)
	}
	if _, ok := discovery.Capabilities.Extensions["io.codemcp.test/skills"]; !ok {
		t.Fatalf("direct discovery omitted feature extension: %#v", discovery.Capabilities.Extensions)
	}

	baseServer, err := NewSDKServerWithProfile(runtime, "base-test", "", "", BaseProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer baseServer.Close()
	openAIServer, err := NewSDKServerWithProfile(runtime, "openai-test", "", "", OpenAIProfile())
	if err != nil {
		t.Fatal(err)
	}
	defer openAIServer.Close()
	if baseServer.FeatureRegistry != registry || openAIServer.FeatureRegistry != registry {
		t.Fatal("profile-specific SDK server created a separate feature registry")
	}

	_, baseOptions := ProjectSDKServer(BaseProfile(), descriptor)
	_, openAIOptions := ProjectSDKServer(OpenAIProfile(), descriptor)
	if !reflect.DeepEqual(baseOptions.Capabilities.Resources, openAIOptions.Capabilities.Resources) ||
		!reflect.DeepEqual(baseOptions.Capabilities.Prompts, openAIOptions.Capabilities.Prompts) ||
		!reflect.DeepEqual(baseOptions.Capabilities.Extensions, openAIOptions.Capabilities.Extensions) {
		t.Fatalf("profile changed feature advertisement: base=%#v openai=%#v", baseOptions.Capabilities, openAIOptions.Capabilities)
	}
	if baseOptions.Capabilities.Resources == nil || !baseOptions.Capabilities.Resources.Subscribe || !baseOptions.Capabilities.Resources.ListChanged ||
		baseOptions.Capabilities.Prompts == nil || !baseOptions.Capabilities.Prompts.ListChanged {
		t.Fatalf("feature capabilities were not projected: %#v", baseOptions.Capabilities)
	}
	if _, ok := baseOptions.Capabilities.Extensions["io.codemcp.test/skills"]; !ok {
		t.Fatalf("skill extension missing from projected capabilities: %#v", baseOptions.Capabilities.Extensions)
	}
	if len(baseOptions.Capabilities.Experimental) != 0 || len(openAIOptions.Capabilities.Experimental) != 0 {
		t.Fatalf("unexpected UI/experimental capability: base=%#v openai=%#v", baseOptions.Capabilities.Experimental, openAIOptions.Capabilities.Experimental)
	}
}

func TestFeatureRegistryRejectsCompetingOwners(t *testing.T) {
	registry := NewFeatureRegistry()
	first := FeatureRegistration{
		ID:     "first",
		Family: FeatureResources,
		Resources: []ResourceDescriptor{{
			URI: "cm://global/shared", Name: "shared", MIMEType: "text/plain",
		}},
		ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
			return TextResourceContent("shared"), nil
		},
		Capabilities: FeatureCapabilities{Extensions: map[string]any{"io.codemcp.test/shared": map[string]any{}}},
		Methods: []FeatureMethod{{
			Name: "io.codemcp.test/shared-method", Scope: FeatureScopeGlobal, Custom: true,
			Handler: func(context.Context, FeatureRequest) (map[string]any, error) { return map[string]any{}, nil },
		}},
	}
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]FeatureRegistration{
		"resource": {
			ID: "resource-owner", Family: FeatureResources,
			Resources: []ResourceDescriptor{{URI: "cm://global/shared", Name: "other", MIMEType: "text/plain"}},
			ReadResource: func(context.Context, ResourceReadRequest) (ResourceContent, error) {
				return TextResourceContent("other"), nil
			},
		},
		"extension": {
			ID: "extension-owner", Family: FeatureSkills,
			Capabilities: FeatureCapabilities{Extensions: map[string]any{"io.codemcp.test/shared": map[string]any{}}},
		},
		"method": {
			ID: "method-owner", Family: FeatureSkills,
			Methods: []FeatureMethod{{
				Name: "io.codemcp.test/shared-method", Scope: FeatureScopeGlobal, Custom: true,
				Handler: func(context.Context, FeatureRequest) (map[string]any, error) { return map[string]any{}, nil },
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := registry.Register(candidate); err == nil {
				t.Fatal("competing feature owner was accepted")
			}
		})
	}
	if snapshot := registry.Snapshot(); len(snapshot.Resources) != 1 || len(snapshot.Methods) != 1 {
		t.Fatalf("failed registration mutated registry: %#v", snapshot)
	}
}

func TestFeatureExecutorSharesWorkspaceAuthorityWithToolFallback(t *testing.T) {
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
		ID: "workspace-owner", Family: FeatureSkills,
		Methods: []FeatureMethod{{
			Name: "io.codemcp.test/workspace", Scope: FeatureScopeWorkspace, Custom: true,
			Handler: func(_ context.Context, request FeatureRequest) (map[string]any, error) {
				calls.Add(1)
				return map[string]any{"workspace_id": request.WorkspaceID}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(registry, runtime, first.ID, "test")
	ctx := tools.WithBoundWorkspace(context.Background(), first.ID)

	_, nativeErr := executor.Invoke(ctx, "io.codemcp.test/workspace", map[string]any{"workspace_id": second.ID})
	var nativeProtocol *Error
	if !errors.As(nativeErr, &nativeProtocol) || nativeProtocol.Code != ErrInvalidParams {
		t.Fatalf("native workspace denial=%#v", nativeErr)
	}

	runtime.Registry.MustRegister("feature_workspace_fallback", tools.Schema{
		Name:        "feature_workspace_fallback",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"workspace_id":{"type":"string"}},"required":["workspace_id"],"additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, executor.ToolFallback("io.codemcp.test/workspace"))
	fallbackResult, fallbackErr := runtime.Call(ctx, "feature_workspace_fallback", map[string]any{"workspace_id": second.ID})
	if fallbackErr != nil || !fallbackResult.IsError || len(fallbackResult.Content) == 0 ||
		!strings.Contains(fallbackResult.Content[0].Text, nativeProtocol.Message) {
		t.Fatalf("fallback workspace denial=%#v err=%v native=%#v", fallbackResult, fallbackErr, nativeProtocol)
	}
	if calls.Load() != 0 {
		t.Fatalf("denied workspace reached feature owner %d times", calls.Load())
	}

	native, err := executor.Invoke(ctx, "io.codemcp.test/workspace", map[string]any{})
	if err != nil || native["workspace_id"] != first.ID {
		t.Fatalf("native authorized result=%#v err=%v", native, err)
	}
	fallback, err := runtime.Call(ctx, "feature_workspace_fallback", map[string]any{"workspace_id": first.ID})
	if err != nil || fallback.IsError {
		t.Fatalf("fallback authorized result=%#v err=%v", fallback, err)
	}
	payload, ok := fallback.StructuredContent.(map[string]any)
	if !ok || payload["workspace_id"] != first.ID {
		t.Fatalf("fallback owner result=%T %#v", fallback.StructuredContent, fallback.StructuredContent)
	}
	if calls.Load() != 2 {
		t.Fatalf("native/fallback did not share one feature owner: calls=%d", calls.Load())
	}
}

func TestFeatureExecutorAppliesOneResultBoundToNativeAndFallback(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), LoopGuard: tools.NewToolLoopGuard()}
	registry := FeatureRegistryForRuntime(runtime)
	if err := registry.Register(FeatureRegistration{
		ID: "bounded-owner", Family: FeatureSkills,
		Methods: []FeatureMethod{{
			Name: "io.codemcp.test/bounded", Scope: FeatureScopeGlobal, Custom: true, MaxResultBytes: 64,
			Handler: func(context.Context, FeatureRequest) (map[string]any, error) {
				return map[string]any{"payload": strings.Repeat("x", 256)}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(registry, runtime, "", "test")
	_, nativeErr := executor.Invoke(context.Background(), "io.codemcp.test/bounded", nil)
	var protocol *Error
	if !errors.As(nativeErr, &protocol) || protocol.Code != ErrInternal || protocol.Message != "feature result exceeds size limit" {
		t.Fatalf("native bounded error=%#v", nativeErr)
	}
	runtime.Registry.MustRegister("feature_bounded_fallback", tools.Schema{
		Name:        "feature_bounded_fallback",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Annotations: tools.ToolAnnotations(tools.RiskRead),
	}, executor.ToolFallback("io.codemcp.test/bounded"))
	result, err := runtime.Call(context.Background(), "feature_bounded_fallback", map[string]any{})
	if err != nil || !result.IsError || len(result.Content) == 0 || result.Content[0].Text != protocol.Message {
		t.Fatalf("fallback bounded result=%#v err=%v", result, err)
	}
}

func TestFeatureExecutorBoundsErrorsAndRejectsNonJSONResults(t *testing.T) {
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), LoopGuard: tools.NewToolLoopGuard()}
	registry := FeatureRegistryForRuntime(runtime)
	if err := registry.Register(FeatureRegistration{
		ID: "error-owner", Family: FeatureSkills,
		Methods: []FeatureMethod{
			{
				Name: "io.codemcp.test/error-bound", Scope: FeatureScopeGlobal, Custom: true, MaxResultBytes: 64,
				Handler: func(context.Context, FeatureRequest) (map[string]any, error) {
					return nil, errors.New(strings.Repeat("error", 64))
				},
			},
			{
				Name: "io.codemcp.test/non-json", Scope: FeatureScopeGlobal, Custom: true,
				Handler: func(context.Context, FeatureRequest) (map[string]any, error) {
					return map[string]any{"invalid": func() {}}, nil
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	executor := NewFeatureExecutor(registry, runtime, "", "test")
	_, err := executor.Invoke(context.Background(), "io.codemcp.test/error-bound", nil)
	var protocol *Error
	if !errors.As(err, &protocol) || protocol.Code != ErrInternal || protocol.Message != "feature error exceeds size limit" {
		t.Fatalf("bounded feature error=%#v", err)
	}
	_, err = executor.Invoke(context.Background(), "io.codemcp.test/non-json", nil)
	if !errors.As(err, &protocol) || protocol.Code != ErrInternal || protocol.Message != "feature result is not JSON serializable" {
		t.Fatalf("non-JSON feature result error=%#v", err)
	}
}

func TestFeatureMethodVisibleThroughOfficialSDKTransportsAndProfiles(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profile   Profile
		transport string
	}{
		{name: "stdio-base", profile: BaseProfile(), transport: "stdio"},
		{name: "streamable-base", profile: BaseProfile(), transport: "streamable"},
		{name: "sse-base", profile: BaseProfile(), transport: "sse"},
		{name: "streamable-openai", profile: OpenAIProfile(), transport: "streamable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := featureTransportRuntime(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var transport sdkmcp.Transport
			var closeServer func()
			if tc.transport == "stdio" {
				clientToServerReader, clientToServerWriter := io.Pipe()
				serverToClientReader, serverToClientWriter := io.Pipe()
				server, err := NewSDKServerWithProfile(runtime, "stdio", "", "", tc.profile)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					done <- server.Server.Run(ctx, &sdkmcp.IOTransport{Reader: clientToServerReader, Writer: serverToClientWriter})
				}()
				transport = &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}
				closeServer = func() {
					server.Close()
					cancel()
					select {
					case <-done:
					case <-time.After(time.Second):
					}
				}
			} else {
				handler, err := NewSDKHTTPHandlerWithProfile(runtime, "", tc.transport == "sse", tc.profile)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewServer(handler)
				if tc.transport == "sse" {
					transport = &sdkmcp.SSEClientTransport{Endpoint: server.URL + "/mcp/sse"}
				} else {
					transport = &sdkmcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", DisableStandaloneSSE: true}
				}
				closeServer = server.Close
			}
			defer closeServer()

			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "feature-registry-test", Version: "1.0.0"}, nil)
			if err := sdkmcp.AddSendingCustomMethod[*featureProbeClientParams, *featureProbeClientResult](client, featureProbeMethod); err != nil {
				t.Fatal(err)
			}
			session, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			initialized := session.InitializeResult()
			if initialized == nil || initialized.Capabilities == nil {
				t.Fatal("initialize capabilities unavailable")
			}
			if _, ok := initialized.Capabilities.Extensions["io.codemcp.test/feature"]; !ok {
				t.Fatalf("feature extension missing on %s/%s: %#v", tc.profile.ID(), tc.transport, initialized.Capabilities.Extensions)
			}
			if tc.transport == "sse" {
				return
			}
			result, err := sdkmcp.CallCustomMethod[*featureProbeClientParams, *featureProbeClientResult](
				ctx, session, featureProbeMethod, &featureProbeClientParams{Value: tc.name},
			)
			if err != nil || result.Value != tc.name {
				t.Fatalf("custom feature result=%#v err=%v", result, err)
			}
		})
	}
}

func featureTransportRuntime(t *testing.T) *tools.Runtime {
	t.Helper()
	runtime := &tools.Runtime{Registry: tools.NewRegistry(), LoopGuard: tools.NewToolLoopGuard()}
	registry := FeatureRegistryForRuntime(runtime)
	if err := registry.Register(FeatureRegistration{
		ID: "transport-feature", Family: FeatureSkills,
		Capabilities: FeatureCapabilities{
			Extensions: map[string]any{"io.codemcp.test/feature": map[string]any{"version": "1"}},
		},
		Methods: []FeatureMethod{{
			Name: featureProbeMethod, Scope: FeatureScopeGlobal, Custom: true,
			Handler: func(_ context.Context, request FeatureRequest) (map[string]any, error) {
				value, _ := request.Params["value"].(string)
				return map[string]any{"value": value}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return runtime
}
