package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestOpenAICompatibleInferenceAdaptsStructuredRequestAndBearerAuth(t *testing.T) {
	const credential = "sk-openai-test-secret"
	const prompt = "openai-prompt-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || request.MaxTokens != 77 || len(request.Messages) != 2 {
			t.Fatalf("request=%#v", request)
		}
		if request.Messages[0].Role != "system" || request.Messages[0].Content != "system instructions" || request.Messages[1].Content != prompt {
			t.Fatalf("messages=%#v", request.Messages)
		}
		if request.ResponseFormat == nil || request.ResponseFormat.Type != "json_schema" || request.ResponseFormat.JSONSchema.Name != "codemcp_response" || !request.ResponseFormat.JSONSchema.Strict {
			t.Fatalf("response_format=%#v", request.ResponseFormat)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"served-model","choices":[{"message":{"content":"{\"ok\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5}}`)
	}))
	defer server.Close()

	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	provider := testProvider(server.URL+"/v1", ProtocolOpenAI, AuthBearer)
	provider.Capabilities = &ProviderCapabilities{StructuredOutput: true}
	result, err := client.Infer(t.Context(), provider, Request{
		Instructions:    "system instructions",
		Messages:        []Message{{Role: RoleUser, Content: prompt}},
		ResponseSchema:  json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
		MaxOutputTokens: 77,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID != provider.ID || result.Model != "served-model" || result.Text != `{"ok":true}` || string(result.Structured) != `{"ok":true}` || result.FinishReason != "stop" {
		t.Fatalf("result=%#v", result)
	}
	if result.Usage == nil || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%#v", result.Usage)
	}
}

func TestOpenAICompatibleInferenceSupportsNoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization=%q", got)
		}
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)
	result, err := NewClient(ClientOptions{}).Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestAnthropicCompatibleInferenceAdaptsMessagesAndAuth(t *testing.T) {
	const credential = "anthropic-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != credential {
			t.Fatalf("x-api-key=%q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != AnthropicAPIVersion {
			t.Fatalf("anthropic-version=%q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization=%q", got)
		}
		var request anthropicMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || request.MaxTokens != DefaultMaxOutputTokens || request.System != "base system\n\nmessage system" {
			t.Fatalf("request=%#v", request)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "user" || request.Messages[1].Role != "assistant" {
			t.Fatalf("messages=%#v", request.Messages)
		}
		_, _ = fmt.Fprint(w, `{"model":"claude-served","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}],"stop_reason":"end_turn","usage":{"input_tokens":13,"output_tokens":2}}`)
	}))
	defer server.Close()

	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	provider := testProvider(server.URL+"/v1", ProtocolAnthropic, AuthAPIKey)
	result, err := client.Infer(t.Context(), provider, Request{
		Instructions: "base system",
		Messages: []Message{
			{Role: RoleSystem, Content: "message system"},
			{Role: RoleUser, Content: "question"},
			{Role: RoleAssistant, Content: "prior answer"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello world" || result.Model != "claude-served" || result.FinishReason != "end_turn" {
		t.Fatalf("result=%#v", result)
	}
	if result.Usage == nil || result.Usage.InputTokens != 13 || result.Usage.OutputTokens != 2 {
		t.Fatalf("usage=%#v", result.Usage)
	}
}

func TestAnthropicCompatibleInferenceSupportsNoAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("no-auth provider received an auth header")
		}
		if r.Header.Get("anthropic-version") != AnthropicAPIVersion {
			t.Fatal("Anthropic API version header missing")
		}
		_, _ = fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolAnthropic, AuthNone)
	result, err := NewClient(ClientOptions{}).Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestOpenAIModelDiscoveryUsesDeclaredCapabilityAndAuth(t *testing.T) {
	const credential = "model-list-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"model-a"},{"id":"model-b"},{"id":"model-a"}]}`)
	}))
	defer server.Close()
	provider := testProvider(server.URL+"/v1", ProtocolOpenAI, AuthBearer)
	provider.Discovery = DiscoveryOpenAIModels
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	models, err := client.DiscoverModels(t.Context(), provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "model-a" || models[1].ID != "model-b" {
		t.Fatalf("models=%#v", models)
	}

	provider.Discovery = DiscoveryNone
	if _, err := client.DiscoverModels(t.Context(), provider); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("unsupported discovery err=%v", err)
	}
	provider.Protocol = ProtocolAnthropic
	provider.Discovery = DiscoveryOpenAIModels
	provider.AuthMode = AuthNone
	if _, err := client.DiscoverModels(t.Context(), provider); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("Anthropic discovery err=%v", err)
	}
}

func TestStructuredOutputRequiresExplicitSupportedCapability(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	request := Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}, ResponseSchema: json.RawMessage(`{"type":"object"}`)}
	client := NewClient(ClientOptions{})

	provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)
	if _, err := client.Infer(t.Context(), provider, request); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("undeclared OpenAI structured output err=%v", err)
	}
	provider.Protocol = ProtocolAnthropic
	provider.Capabilities = &ProviderCapabilities{StructuredOutput: true}
	if _, err := client.Infer(t.Context(), provider, request); !IsCategory(err, ErrorUnsupported) {
		t.Fatalf("unimplemented Anthropic structured output err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("unsupported structured requests reached network %d time(s)", calls.Load())
	}
}

func TestInferenceMapsHTTPAndResponseErrorsWithoutRetry(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		category ErrorCategory
	}{
		{name: "bad_request", status: http.StatusBadRequest, body: `{"error":{"message":"secret echoed prompt"}}`, category: ErrorInvalidRequest},
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"bad key"}`, category: ErrorUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"forbidden"}`, category: ErrorUnauthorized},
		{name: "rate_limited", status: http.StatusTooManyRequests, body: `{"error":"slow down"}`, category: ErrorRateLimited},
		{name: "server_error", status: http.StatusInternalServerError, body: `{"error":"down"}`, category: ErrorUnavailable},
		{name: "bad_gateway", status: http.StatusBadGateway, body: `{"error":"down"}`, category: ErrorUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)
			_, err := NewClient(ClientOptions{}).Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "prompt secret"}}})
			if !IsCategory(err, test.category) {
				t.Fatalf("err=%v category=%s", err, test.category)
			}
			if calls.Load() != 1 {
				t.Fatalf("network calls=%d want=1", calls.Load())
			}
			if strings.Contains(err.Error(), test.body) || strings.Contains(err.Error(), "prompt secret") {
				t.Fatalf("error leaked provider content: %v", err)
			}
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{not-json`)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)
	if _, err := NewClient(ClientOptions{}).Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorInvalidResponse) {
		t.Fatalf("malformed JSON err=%v", err)
	}
}

func TestInferenceMapsTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"late"}}]}`)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)
	client := NewClient(ClientOptions{Timeout: 20 * time.Millisecond})
	if _, err := client.Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorTimeout) {
		t.Fatalf("timeout err=%v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewClient(ClientOptions{}).Infer(ctx, provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorCancelled) {
		t.Fatalf("cancel err=%v", err)
	}
}

func TestInferenceBoundsSerializedRequestAndResponse(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"`+strings.Repeat("x", 512)+`"}}]}`)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolOpenAI, AuthNone)

	requestClient := NewClient(ClientOptions{MaxRequestBytes: 128})
	if _, err := requestClient.Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: strings.Repeat("p", 512)}}}); !IsCategory(err, ErrorInvalidRequest) {
		t.Fatalf("oversized request err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized request reached network %d time(s)", calls.Load())
	}

	responseClient := NewClient(ClientOptions{MaxResponseBytes: 128})
	if _, err := responseClient.Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorInvalidResponse) {
		t.Fatalf("oversized response err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("response network calls=%d", calls.Load())
	}
}

func TestInferenceTraceContainsOnlySafeMetadata(t *testing.T) {
	const credential = "sk-trace-secret"
	const prompt = "prompt-trace-sentinel"
	const output = "output-trace-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"model":"served-model","choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`, output)
	}))
	defer server.Close()
	provider := testProvider(server.URL, ProtocolOpenAI, AuthBearer)
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	var events []tracepkg.Event
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	if _, err := client.Infer(ctx, provider, Request{Instructions: "system-trace-sentinel", Messages: []Message{{Role: RoleUser, Content: prompt}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{credential, prompt, output, "system-trace-sentinel"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("trace leaked %q: %s", forbidden, text)
		}
	}
	for _, expected := range []string{"provider_id", "test-model", "input_tokens", "output_tokens"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("trace missing safe metadata %q: %s", expected, text)
		}
	}
}

func TestInferenceDoesNotFollowRedirectsWithCredentials(t *testing.T) {
	const credential = "redirect-secret-key"
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if r.Header.Get("x-api-key") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("credential reached redirected host")
		}
		_, _ = fmt.Fprint(w, `{"content":[{"type":"text","text":"unexpected"}]}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != credential {
			t.Fatalf("source x-api-key=%q", r.Header.Get("x-api-key"))
		}
		http.Redirect(w, r, target.URL+"/messages", http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	provider := testProvider(source.URL, ProtocolAnthropic, AuthAPIKey)
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return credential, nil }})
	_, err := client.Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if !IsCategory(err, ErrorProvider) {
		t.Fatalf("redirect err=%v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target calls=%d", targetCalls.Load())
	}
}

func TestCredentialResolverFailureDoesNotLeakResolverDetail(t *testing.T) {
	const secret = "resolver-secret-sentinel"
	provider := testProvider("https://example.test/v1", ProtocolOpenAI, AuthBearer)
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) {
		return "", NewError(ErrorUnavailable, "api_key", "backend failed with "+secret)
	}})
	var events []tracepkg.Event
	ctx := tracepkg.WithObserver(t.Context(), func(event tracepkg.Event) { events = append(events, event) })
	_, err := client.Infer(ctx, provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}})
	if !IsCategory(err, ErrorUnavailable) {
		t.Fatalf("credential resolver err=%v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credential error leaked resolver detail: %v", err)
	}
	raw, marshalErr := json.Marshal(events)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("credential trace leaked resolver detail: %s", raw)
	}
}

func TestInferenceRejectsProtocolAuthMismatchAndUnsupportedRole(t *testing.T) {
	client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) { return "secret", nil }})
	openAI := testProvider("https://example.test/v1", ProtocolOpenAI, AuthAPIKey)
	if _, err := client.Infer(t.Context(), openAI, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorMisconfigured) {
		t.Fatalf("OpenAI auth mismatch err=%v", err)
	}
	anthropic := testProvider("https://example.test/v1", ProtocolAnthropic, AuthBearer)
	if _, err := client.Infer(t.Context(), anthropic, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); !IsCategory(err, ErrorMisconfigured) {
		t.Fatalf("Anthropic auth mismatch err=%v", err)
	}
	valid := testProvider("https://example.test/v1", ProtocolOpenAI, AuthNone)
	if _, err := client.Infer(t.Context(), valid, Request{Messages: []Message{{Role: RoleTool, Content: "tool output"}}}); !IsCategory(err, ErrorInvalidRequest) {
		t.Fatalf("unsupported role err=%v", err)
	}
}

func testProvider(baseURL string, protocol Protocol, auth AuthMode) Provider {
	return Provider{
		ID: "test-provider", Name: "Test Provider", Protocol: protocol, BaseURL: baseURL,
		Model: "test-model", AuthMode: auth, Discovery: DiscoveryNone,
	}
}
