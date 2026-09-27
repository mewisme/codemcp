package typesafe

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/integrations/semantic"
)

const systemOneTestKey = "typesafe-systemone-test-secret"

func systemOneRequest() semantic.Request {
	return semantic.Request{
		Consumer: semantic.Consumer{ID: "memory.search", Purpose: "memory_rerank"},
		State:    map[string]any{"query": "postgres", "candidate": map[string]any{"note": "Use PostgreSQL"}},
		Questions: map[string]semantic.Question{
			"relevant": {Type: semantic.PrimitiveNoul, Instructions: "Is the candidate relevant?", Noul: &semantic.NoulCriteria{True: "Relevant", False: "Irrelevant"}},
			"route":    {Type: semantic.PrimitiveChoice, Instructions: "Choose a route", Choice: map[string]any{"keep": "Keep candidate", "skip": "Skip candidate"}},
			"quality": {Type: semantic.PrimitiveScore, Instructions: "Rate candidate quality", Score: []semantic.ScoreLevel{
				{Key: "low", Value: 10, Description: "Low quality"},
				{Key: "medium", Value: 20, Description: "Medium quality"},
				{Key: "high", Value: 30, Description: "High quality"},
			}},
		},
	}
}

func riskInput(command string) semantic.RiskInput {
	return semantic.RiskInput{
		Consumer:    semantic.Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		WorkspaceID: "ws_demo", CallerID: "caller_demo",
		Invocation: semantic.CanonicalInvocation{
			Operation: "run_command", Tool: "run_command",
			Arguments: map[string]any{"command": command},
		},
	}
}

func TestSystemOneClientMapsNeutralSemanticRoundTrip(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != SystemOnePath || r.Header.Get("Authorization") != "Bearer "+systemOneTestKey {
			t.Fatalf("request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"relevant":{"type":"noul","noul":0.8},"route":{"type":"choice","choice":"keep","probabilities":{"keep":0.75,"skip":0.25},"confidence":0.7},"quality":{"type":"score","score":1.25,"legend":{"0":"Low quality","1":"Medium quality","2":"High quality"},"probabilities":{"0":0,"1":0.75,"2":0.25},"confidence":0.6}},"usage":{"input_tokens":123,"output_tokens":17}}`)
	}))
	defer server.Close()
	client, err := NewClientWithOptions(systemOneTestKey, DefaultModel, 2*time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Evaluate(t.Context(), systemOneRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "typesafe" || result.Model != "jev-1.13.0" || result.Usage == nil || result.Usage.InputTokens != 123 {
		t.Fatalf("result=%#v", result)
	}
	if got := result.Answers["quality"].Score; got.Score != 22.5 || got.Probabilities["high"] != 0.25 {
		t.Fatalf("score=%#v", got)
	}
	if received["model"] != DefaultModel {
		t.Fatalf("wire model=%#v", received["model"])
	}
}

func TestSystemOneClientClassifiesCanonicalRiskWithChoiceContract(t *testing.T) {
	const command = "git clean -fdx"
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"risk_class":{"type":"choice","choice":"high","probabilities":{"low":0.01,"medium":0.09,"high":0.85,"critical":0.05},"confidence":0.91}},"usage":{"input_tokens":40,"output_tokens":4}}`)
	}))
	defer server.Close()
	client, err := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	input := riskInput(command)
	assessment, err := client.ClassifyRisk(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Class != semantic.RiskHigh || assessment.Confidence != 0.91 || assessment.Category != riskCategory || assessment.Provider.Provider != "typesafe" {
		t.Fatalf("assessment=%#v", assessment)
	}
	state := received["state"].(map[string]any)
	if _, exists := state["workspace_id"]; exists {
		t.Fatal("workspace metadata was sent despite not being needed for risk judgment")
	}
	invocation := state["invocation"].(map[string]any)
	arguments := invocation["arguments"].(map[string]any)
	if arguments["command"] != command {
		t.Fatalf("canonical command changed: %#v", arguments["command"])
	}
	questions := received["questions"].(map[string]any)
	criteria := questions[riskQuestionID].(map[string]any)["criteria"].(map[string]any)
	if len(criteria) != 4 {
		t.Fatalf("risk criteria=%#v", criteria)
	}
}

func TestSystemOneRiskPreservesLowConfidenceForManagerValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"risk_class":{"type":"choice","choice":"medium","probabilities":{"low":0.2,"medium":0.4,"high":0.3,"critical":0.1},"confidence":0.35}},"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	input := riskInput("rm file")
	assessment, err := client.ClassifyRisk(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Confidence != 0.35 {
		t.Fatalf("confidence=%v", assessment.Confidence)
	}
	if err := semantic.ValidateRiskAssessment(input, assessment, 0.8); !semantic.IsCategory(err, semantic.ErrorInvalidResponse) {
		t.Fatalf("low confidence was not rejected by neutral contract: %#v", err)
	}
}

func TestSystemOneClientMapsFailuresForSemanticAndRiskWithoutLeaks(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   semantic.ErrorCategory
	}{
		{http.StatusUnauthorized, semantic.ErrorUnauthorized},
		{http.StatusUnprocessableEntity, semantic.ErrorInvalidRequest},
		{http.StatusTooManyRequests, semantic.ErrorRateLimited},
		{529, semantic.ErrorOverloaded},
		{http.StatusInternalServerError, semantic.ErrorProvider},
	} {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "provider-body-sentinel")
			}))
			defer server.Close()
			client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
			_, evalErr := client.Evaluate(t.Context(), systemOneRequest())
			_, riskErr := client.ClassifyRisk(t.Context(), riskInput("raw-command-sentinel"))
			for _, err := range []error{evalErr, riskErr} {
				if !semantic.IsCategory(err, tc.want) {
					t.Fatalf("err=%#v want=%s", err, tc.want)
				}
				text := err.Error()
				for _, secret := range []string{systemOneTestKey, "provider-body-sentinel", "raw-command-sentinel"} {
					if strings.Contains(text, secret) {
						t.Fatalf("error leaked sensitive content: %v", err)
					}
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("client retried internally: calls=%d", calls.Load())
			}
		})
	}
}

func TestSystemOneClientRejectsMalformedSemanticResponses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "content type", contentType: "text/plain", body: "{}"},
		{name: "malformed json", contentType: "application/json", body: `{"model":`},
		{name: "missing usage", contentType: "application/json", body: `{"model":"jev-1.13.0","answers":{}}`},
		{name: "invalid noul", contentType: "application/json", body: `{"model":"jev-1.13.0","answers":{"relevant":{"type":"noul","noul":1.5},"route":{"type":"choice","choice":"keep","probabilities":{"keep":0.75,"skip":0.25},"confidence":0.7},"quality":{"type":"score","score":1,"legend":{"0":"a","1":"b","2":"c"},"probabilities":{"0":0,"1":1,"2":0},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
			if _, err := client.Evaluate(t.Context(), systemOneRequest()); !semantic.IsCategory(err, semantic.ErrorInvalidResponse) {
				t.Fatalf("err=%#v", err)
			}
		})
	}
}

func TestSystemOneClientRejectsMalformedUnknownAndOversizedRiskResponses(t *testing.T) {
	for _, body := range []string{
		`{"model":"jev-1.13.0","answers":{"risk_class":{"type":"choice","choice":"extreme","probabilities":{"low":0,"medium":0,"high":0,"critical":0},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"model":"jev-1.13.0","answers":{"risk_class":{"type":"choice","choice":"high","probabilities":{"high":1},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
		_, err := client.ClassifyRisk(t.Context(), riskInput("echo safe"))
		server.Close()
		if !semantic.IsCategory(err, semantic.ErrorInvalidResponse) {
			t.Fatalf("body=%s err=%#v", body, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Repeat("x", maxSystemOneResponseBody+1))
	}))
	defer server.Close()
	client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	if _, err := client.ClassifyRisk(t.Context(), riskInput("echo safe")); !semantic.IsCategory(err, semantic.ErrorInvalidResponse) {
		t.Fatalf("oversized err=%#v", err)
	}
}

func TestSystemOneClientRejectsOversizedRequestBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	request := semantic.Request{
		Consumer: semantic.Consumer{ID: "memory.search", Purpose: "memory_rerank"},
		State:    map[string]any{"query": "bounded"},
		Questions: map[string]semantic.Question{
			"large": {Type: semantic.PrimitiveChoice, Instructions: "choose", Choice: map[string]any{}},
		},
	}
	for i := 0; i < semantic.MaxCriteriaEntries; i++ {
		request.Questions["large"].Choice[fmt.Sprintf("option_%d", i)] = strings.Repeat("x", 12<<10)
	}
	if _, err := client.Evaluate(t.Context(), request); !semantic.IsCategory(err, semantic.ErrorInvalidRequest) {
		t.Fatalf("err=%#v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized request reached network: calls=%d", calls.Load())
	}
}

func TestSystemOneClientCancellationDeadlineAndRedirectBoundBothCapabilities(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{},"usage":{"input_tokens":0,"output_tokens":0}}`)
	}))
	defer slow.Close()
	client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, 30*time.Millisecond, ClientOptions{HTTPClient: slow.Client(), BaseURL: slow.URL})
	if _, err := client.Evaluate(t.Context(), systemOneRequest()); !semantic.IsCategory(err, semantic.ErrorTimeout) {
		t.Fatalf("evaluation deadline err=%#v", err)
	}
	if _, err := client.ClassifyRisk(t.Context(), riskInput("touch file")); !semantic.IsCategory(err, semantic.ErrorTimeout) {
		t.Fatalf("risk deadline err=%#v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Evaluate(ctx, systemOneRequest()); !semantic.IsCategory(err, semantic.ErrorCancelled) {
		t.Fatalf("evaluation cancellation err=%#v", err)
	}
	if _, err := client.ClassifyRisk(ctx, riskInput("touch file")); !semantic.IsCategory(err, semantic.ErrorCancelled) {
		t.Fatalf("risk cancellation err=%#v", err)
	}

	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	redirectTarget := strings.Replace(target.URL, "127.0.0.1", "localhost", 1) + SystemOnePath
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	redirectClient, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: redirector.Client(), BaseURL: redirector.URL})
	if _, err := redirectClient.ClassifyRisk(t.Context(), riskInput("touch file")); !semantic.IsCategory(err, semantic.ErrorTransport) {
		t.Fatalf("redirect err=%#v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("cross-host redirect forwarded credential: target calls=%d", targetCalls.Load())
	}
}

func TestSystemOneProductionEndpointIsFixedHTTPS(t *testing.T) {
	client, err := NewClient(systemOneTestKey, DefaultModel, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.endpoint != DefaultBaseURL+SystemOnePath {
		t.Fatalf("endpoint=%q", client.endpoint)
	}
	for _, baseURL := range []string{"ftp://api.typesafe.ai", "https://user:pass@api.typesafe.ai", "https://api.typesafe.ai/other"} {
		if _, err := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{BaseURL: baseURL}); err == nil {
			t.Fatalf("invalid base URL accepted: %s", baseURL)
		}
	}
}

func TestSystemOneManagerOwnsRetriesForBothCapabilities(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, _ := NewClientWithOptions(systemOneTestKey, DefaultModel, time.Second, ClientOptions{HTTPClient: server.Client(), BaseURL: server.URL})
	manager := semantic.NewManager(semantic.ManagerOptions{
		MaxAttempts:    3,
		RetryBaseDelay: time.Nanosecond,
		Sleep:          func(context.Context, time.Duration) error { return nil },
	})
	if err := manager.RegisterProvider("typesafe", semantic.ProviderRegistration{Provider: client, RiskClassifier: client, Model: DefaultModel}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SelectProvider("typesafe"); err != nil {
		t.Fatal(err)
	}
	_, _ = manager.Evaluate(t.Context(), systemOneRequest())
	if calls.Load() != 3 {
		t.Fatalf("evaluation calls=%d want manager-owned 3", calls.Load())
	}
	calls.Store(0)
	_, _ = manager.ClassifyRisk(t.Context(), riskInput("touch file"), 0.8)
	if calls.Load() != 3 {
		t.Fatalf("risk calls=%d want manager-owned 3", calls.Load())
	}
}

func TestTypeSafeProductionSourcesDoNotOwnTraceTelemetryOrApprovalEvents(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			for _, forbidden := range []string{"/trace", "/telemetry", "/approval", "/controlguard", "/tools"} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("%s imports forbidden observability/policy owner %s", entry.Name(), path)
				}
			}
		}
	}
}
