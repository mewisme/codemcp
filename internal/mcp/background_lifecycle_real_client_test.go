package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"go.mewis.me/codemcp/internal/tools"
)

const (
	backgroundLifecycleEvidenceDate   = "2026-09-26"
	backgroundLifecycleEvidenceClient = "github.com/modelcontextprotocol/go-sdk v1.7.0"
)

func TestBackgroundLifecycleRealClientEvidenceDoesNotOverclaimContinuation(t *testing.T) {
	if backgroundLifecycleEvidenceDate != "2026-09-26" || backgroundLifecycleEvidenceClient != "github.com/modelcontextprotocol/go-sdk v1.7.0" {
		t.Fatal("background lifecycle evidence metadata changed without updating the dated compatibility baseline")
	}

	t.Run("base-stdio", func(t *testing.T) {
		runtime := backgroundEvidenceRuntime(t)
		clientToServerReader, clientToServerWriter := io.Pipe()
		serverToClientReader, serverToClientWriter := io.Pipe()
		server, err := NewStdioRuntime(runtime, clientToServerReader, serverToClientWriter)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		serverDone := make(chan error, 1)
		go func() { serverDone <- server.Run(ctx) }()
		client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "background-evidence-stdio", Version: "1.0.0"}, nil)
		session, err := client.Connect(ctx, &sdkmcp.IOTransport{Reader: serverToClientReader, Writer: clientToServerWriter}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertRealClientBackgroundFallback(t, ctx, session, BaseProfile())
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-serverDone:
			if err != nil && err != context.Canceled {
				t.Fatalf("stdio server: %v", err)
			}
		case <-time.After(time.Second):
			cancel()
		}
	})

	for _, test := range []struct {
		name      string
		profile   Profile
		legacySSE bool
		transport func(string) sdkmcp.Transport
	}{
		{
			name:    "base-streamable-http",
			profile: BaseProfile(),
			transport: func(endpoint string) sdkmcp.Transport {
				return &sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", DisableStandaloneSSE: true}
			},
		},
		{
			name:      "base-legacy-sse",
			profile:   BaseProfile(),
			legacySSE: true,
			transport: func(endpoint string) sdkmcp.Transport {
				return &sdkmcp.SSEClientTransport{Endpoint: endpoint + "/mcp/sse"}
			},
		},
		{
			name:    "openai-streamable-http",
			profile: OpenAIProfile(),
			transport: func(endpoint string) sdkmcp.Transport {
				return &sdkmcp.StreamableClientTransport{Endpoint: endpoint + "/mcp", DisableStandaloneSSE: true}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := backgroundEvidenceRuntime(t)
			handler, err := NewSDKHTTPHandlerWithProfile(runtime, "", test.legacySSE, test.profile)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "background-evidence-" + test.name, Version: "1.0.0"}, nil)
			session, err := client.Connect(ctx, test.transport(server.URL), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			assertRealClientBackgroundFallback(t, ctx, session, test.profile)
		})
	}
}

func backgroundEvidenceRuntime(t *testing.T) *tools.Runtime {
	t.Helper()
	registry := tools.NewRegistry()
	registry.MustRegister("background_capability_probe", tools.Schema{
		Name:        "background_capability_probe",
		Description: "Return negotiated background capability truth.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}, func(ctx context.Context, _ map[string]any) (tools.Result, error) {
		capabilities := tools.BackgroundCapabilitiesFromContext(ctx)
		return tools.JSONResult(map[string]any{
			"task_observation":    capabilities.TaskObservation,
			"server_notification": capabilities.ServerNotification,
			"model_continuation":  capabilities.ModelContinuation,
			"in_flight_steering":  capabilities.InFlightSteering,
		}), nil
	})
	return &tools.Runtime{Registry: registry, LoopGuard: tools.NewToolLoopGuard()}
}

func assertRealClientBackgroundFallback(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, profile Profile) {
	t.Helper()
	initialized := session.InitializeResult()
	if initialized == nil {
		t.Fatal("client initialize result is unavailable")
	}
	if initialized.Instructions != ProjectServerInstructions(profile) {
		t.Fatalf("client instructions drifted for profile %q", profile.ID())
	}
	for _, required := range []string{
		"Do not poll process_status/process_output or Tasks to wait.",
		"No model continuation is proven, so return control after starting background work.",
	} {
		if !strings.Contains(initialized.Instructions, required) {
			t.Fatalf("client instructions for profile %q omitted fallback guidance %q: %s", profile.ID(), required, initialized.Instructions)
		}
	}
	if strings.Contains(initialized.Instructions, "The client proves model continuation") {
		t.Fatalf("client instructions overclaimed continuation for profile %q: %s", profile.ID(), initialized.Instructions)
	}

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "background_capability_probe", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("background capability probe=%#v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("background capability content=%#v", result.Content)
	}
	var capabilities struct {
		TaskObservation    bool `json:"task_observation"`
		ServerNotification bool `json:"server_notification"`
		ModelContinuation  bool `json:"model_continuation"`
		InFlightSteering   bool `json:"in_flight_steering"`
	}
	if err := json.Unmarshal([]byte(text.Text), &capabilities); err != nil {
		t.Fatal(err)
	}
	if capabilities.ModelContinuation || capabilities.InFlightSteering {
		t.Fatalf("real client observed unproven continuation capabilities for profile %q: %#v", profile.ID(), capabilities)
	}
}
