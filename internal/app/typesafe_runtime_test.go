package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/integrations/semantic"
	typesafeintegration "go.mewis.me/codemcp/internal/integrations/typesafe"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/tools"
)

type typeSafeWireObservation struct {
	Authorization string
	Model         string
	Risk          bool
	Command       string
}

type typeSafeFakeServer struct {
	server       *httptest.Server
	mu           sync.Mutex
	observations []typeSafeWireObservation
}

func newTypeSafeFakeServer(t *testing.T) *typeSafeFakeServer {
	t.Helper()
	fake := &typeSafeFakeServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != typesafeintegration.SystemOnePath {
			t.Fatalf("unexpected TypeSafe request: %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			State     map[string]any            `json:"state"`
			Model     string                    `json:"model"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		observation := typeSafeWireObservation{
			Authorization: r.Header.Get("Authorization"),
			Model:         request.Model,
		}
		answers := make(map[string]any, len(request.Questions))
		for id, question := range request.Questions {
			typeName, _ := question["type"].(string)
			if id == "risk_class" {
				observation.Risk = true
				observation.Command = typeSafeRiskCommand(request.State)
				class := typeSafeRiskClass(observation.Command)
				answers[id] = map[string]any{
					"type": typeName, "choice": class,
					"probabilities": map[string]float64{
						"low":      riskProbability(class, "low"),
						"medium":   riskProbability(class, "medium"),
						"high":     riskProbability(class, "high"),
						"critical": riskProbability(class, "critical"),
					},
					"confidence": 0.96,
				}
				continue
			}
			answers[id] = map[string]any{"type": typeName, "noul": 0.9}
		}
		fake.mu.Lock()
		fake.observations = append(fake.observations, observation)
		fake.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   request.Model,
			"answers": answers,
			"usage":   map[string]int{"input_tokens": 8, "output_tokens": max(1, len(answers))},
		})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (s *typeSafeFakeServer) snapshot() []typeSafeWireObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]typeSafeWireObservation(nil), s.observations...)
}

func typeSafeRiskCommand(state map[string]any) string {
	invocation, _ := state["invocation"].(map[string]any)
	arguments, _ := invocation["arguments"].(map[string]any)
	command, _ := arguments["command"].(string)
	return command
}

func typeSafeRiskClass(command string) string {
	for _, class := range []string{"critical", "high", "medium", "low"} {
		if strings.Contains(command, "risk-"+class) {
			return class
		}
	}
	return "low"
}

func riskProbability(selected, current string) float64 {
	if selected == current {
		return 0.97
	}
	return 0.01
}

func isolateTypeSafeApp(t *testing.T) config.Config {
	t.Helper()
	t.Setenv("CM_CONFIG_DIR", t.TempDir())
	cleanup := secretstore.UseMemoryForTesting()
	t.Cleanup(cleanup)
	cfg := config.Default()
	cfg.HTTP.Admin.Enabled = false
	cfg.HTTP.MCP.Auth.Enabled = false
	cfg.HTTP.Admin.Auth.Enabled = false
	cfg.HTTP.Security.AllowUnauthenticatedLoopback = true
	cfg.Integrations.RTK.Enabled = false
	cfg.Integrations.CodeGraph.Enabled = false
	return cfg
}

func TestTypeSafeRuntimeDisabledAndMissingCredentialAreExplicit(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	cfg.Integrations.TypeSafe.Enabled = false
	disabled, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	health := disabled.Tools.Semantic.Health()
	if health.Available || health.LastErrorCategory != semantic.ErrorDisabled {
		t.Fatalf("disabled semantic health=%#v", health)
	}

	cfg.Integrations.TypeSafe.Enabled = true
	missing, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	health = missing.Tools.Semantic.Health()
	if health.Available || health.LastErrorCategory != semantic.ErrorMisconfigured {
		t.Fatalf("missing-key semantic health=%#v", health)
	}
}

func TestTypeSafeRuntimeWiresMemorySearchAndApprovalRiskThroughOneSystemOneClient(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	fake := newTypeSafeFakeServer(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app.typeSafeHTTPClient = fake.server.Client()
	app.typeSafeBaseURL = fake.server.URL
	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "generation-one"); err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Integrations.TypeSafe.Enabled = true
	next.Approval.Semantic.Enabled = true
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if health := app.Tools.Semantic.Health(); !health.Available || health.Provider != typeSafeSemanticProvider || health.Model != next.Integrations.TypeSafe.Model {
		t.Fatalf("ready semantic health=%#v", health)
	}

	root := t.TempDir()
	workspace, err := app.Tools.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	for index, note := range []string{"PostgreSQL migration notes", "Frontend theme notes"} {
		result, err := app.Tools.Call(context.Background(), "remember", map[string]any{
			"workspace_id": workspace.ID,
			"scope":        "typesafe-runtime",
			"key":          "note-" + string(rune('a'+index)),
			"note":         note,
		})
		if err != nil || result.IsError {
			t.Fatalf("remember result=%#v err=%v", result, err)
		}
	}
	search, err := app.Tools.Call(context.Background(), "memory_search", map[string]any{
		"workspace_id": workspace.ID,
		"query":        "database migration",
		"limit":        2,
	})
	if err != nil || search.IsError {
		t.Fatalf("memory search=%#v err=%v", search, err)
	}
	searchResult, ok := search.StructuredContent.(tools.MemorySearchResult)
	if !ok || searchResult.Semantic == nil || !searchResult.Semantic.Used || searchResult.Semantic.Fallback || searchResult.Semantic.Provider != typeSafeSemanticProvider {
		t.Fatalf("memory search semantic=%#v", search.StructuredContent)
	}
	project, err := app.Tools.Call(context.Background(), "project_context", map[string]any{
		"workspace_id": workspace.ID,
		"memory_query": "database migration",
	})
	if err != nil || project.IsError {
		t.Fatalf("project context=%#v err=%v", project, err)
	}
	projectResult, ok := project.StructuredContent.(tools.ProjectContextResult)
	if !ok || !projectResult.Summary.Semantic.Used || projectResult.Summary.Semantic.Fallback || projectResult.Summary.Semantic.Provider != typeSafeSemanticProvider {
		t.Fatalf("project context semantic=%#v", project.StructuredContent)
	}

	for _, class := range []string{"low", "medium", "high", "critical"} {
		target := filepath.Join(root, "risk-"+class)
		ctx := tools.WithCallSource(context.Background(), "tunnel")
		ctx = tools.WithApprovalCorrelation(ctx, "typesafe-"+class, "request-"+class)
		result, err := app.Tools.Call(ctx, "run_command", map[string]any{
			"workspace_id": workspace.ID,
			"command":      "touch " + filepath.Base(target),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Stat(target)
		switch class {
		case "low":
			if result.IsError || statErr != nil {
				t.Fatalf("low-risk production classification result=%#v stat=%v", result, statErr)
			}
		case "medium", "high":
			if !result.IsError || result.StructuredContent == nil || !os.IsNotExist(statErr) {
				t.Fatalf("%s-risk review result=%#v stat=%v", class, result, statErr)
			}
		case "critical":
			if !result.IsError || result.StructuredContent != nil || !os.IsNotExist(statErr) {
				t.Fatalf("critical-risk deny result=%#v stat=%v", result, statErr)
			}
		}
	}

	backgroundTarget := filepath.Join(root, "risk-high-background")
	backgroundCtx := tools.WithCallSource(context.Background(), "tunnel")
	backgroundCtx = tools.WithApprovalCorrelation(backgroundCtx, "typesafe-background", "request-background")
	background, err := app.Tools.Call(backgroundCtx, "start_process", map[string]any{
		"workspace_id": workspace.ID,
		"command":      "touch " + filepath.Base(backgroundTarget),
	})
	if err != nil || !background.IsError || background.StructuredContent == nil {
		t.Fatalf("background TypeSafe classification result=%#v err=%v", background, err)
	}
	if _, err := os.Stat(backgroundTarget); !os.IsNotExist(err) {
		t.Fatalf("background command executed despite high-risk review: %v", err)
	}

	observations := fake.snapshot()
	semanticCalls, riskCalls := 0, 0
	backgroundRiskSeen := false
	for _, observation := range observations {
		if observation.Authorization != "Bearer generation-one" {
			t.Fatalf("unexpected credential generation: %#v", observation)
		}
		if observation.Risk {
			riskCalls++
			if strings.Contains(observation.Command, filepath.Base(backgroundTarget)) {
				backgroundRiskSeen = true
			}
		} else {
			semanticCalls++
		}
	}
	if semanticCalls == 0 || riskCalls != 5 || !backgroundRiskSeen {
		t.Fatalf("System One calls semantic=%d risk=%d background=%t observations=%#v", semanticCalls, riskCalls, backgroundRiskSeen, observations)
	}
}

func TestTypeSafeRuntimeCredentialRotationReplacesBothCapabilitiesTogether(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	fake := newTypeSafeFakeServer(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app.typeSafeHTTPClient = fake.server.Client()
	app.typeSafeBaseURL = fake.server.URL
	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "generation-one"); err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Integrations.TypeSafe.Enabled = true
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}

	request := semantic.Request{
		Consumer: semantic.Consumer{ID: "runtime.rotation", Purpose: "generation_check"},
		State:    map[string]any{"value": "same-request"},
		Questions: map[string]semantic.Question{
			"relevance": {Type: semantic.PrimitiveNoul, Instructions: "Is the value relevant?"},
		},
	}
	input := semantic.RiskInput{
		Consumer: semantic.Consumer{ID: "approval.semantic", Purpose: "command_risk"},
		Invocation: semantic.CanonicalInvocation{
			Operation: "run_command", Tool: "run_command",
			Arguments: map[string]any{"command": "touch risk-low-before"},
		},
	}
	if _, err := app.Tools.Semantic.Evaluate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Tools.Semantic.ClassifyRisk(t.Context(), input, 0.8); err != nil {
		t.Fatal(err)
	}
	cut := len(fake.snapshot())

	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "generation-two"); err != nil {
		t.Fatal(err)
	}
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Tools.Semantic.Evaluate(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	input.Invocation.Arguments = map[string]any{"command": "touch risk-low-after"}
	if _, err := app.Tools.Semantic.ClassifyRisk(t.Context(), input, 0.8); err != nil {
		t.Fatal(err)
	}

	after := fake.snapshot()
	if len(after) <= cut {
		t.Fatalf("credential rotation did not invalidate old semantic generation: before=%d after=%d", cut, len(after))
	}
	seenEvaluation, seenRisk := false, false
	for _, observation := range after[cut:] {
		if observation.Authorization != "Bearer generation-two" {
			t.Fatalf("mixed TypeSafe credential generation after rotation: %#v", observation)
		}
		if observation.Risk {
			seenRisk = true
		} else {
			seenEvaluation = true
		}
	}
	if !seenEvaluation || !seenRisk {
		t.Fatalf("rotation did not exercise both capabilities: %#v", after[cut:])
	}
}

func TestTypeSafeRuntimeReconcilesCredentialRemovalAndRestoreWithoutPreferenceToggle(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	fake := newTypeSafeFakeServer(t)
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app.typeSafeHTTPClient = fake.server.Client()
	app.typeSafeBaseURL = fake.server.URL

	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "generation-one"); err != nil {
		t.Fatal(err)
	}
	if err := app.ReloadConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if health := app.Tools.Semantic.Health(); !health.Available {
		t.Fatalf("configured TypeSafe did not activate: %#v", health)
	}

	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), ""); err != nil {
		t.Fatal(err)
	}
	if err := app.ReloadConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if health := app.Tools.Semantic.Health(); health.Available || health.LastErrorCategory != semantic.ErrorMisconfigured {
		t.Fatalf("removed TypeSafe credential did not deactivate provider: %#v", health)
	}
	if snapshot := app.Config.Snapshot(); !snapshot.Integrations.TypeSafe.Enabled || !snapshot.Approval.Semantic.Enabled {
		t.Fatalf("credential removal changed persisted enabled intent: %#v", snapshot)
	}

	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "generation-two"); err != nil {
		t.Fatal(err)
	}
	if err := app.ReloadConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if health := app.Tools.Semantic.Health(); !health.Available || health.Provider != typeSafeSemanticProvider {
		t.Fatalf("restored TypeSafe credential did not reactivate provider: %#v", health)
	}
}

func TestTypeSafeProductionFailuresPreserveSemanticFallbackAndRequireRiskReview(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			State     map[string]any            `json:"state"`
			Model     string                    `json:"model"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if _, risk := request.Questions["risk_class"]; !risk {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		command := typeSafeRiskCommand(request.State)
		if !strings.Contains(command, "risk-low-confidence") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": request.Model,
			"answers": map[string]any{
				"risk_class": map[string]any{
					"type": "choice", "choice": "low",
					"probabilities": map[string]float64{"low": 0.4, "medium": 0.3, "high": 0.2, "critical": 0.1},
					"confidence":    0.2,
				},
			},
			"usage": map[string]int{"input_tokens": 4, "output_tokens": 1},
		})
	}))
	defer server.Close()

	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	app.typeSafeHTTPClient = server.Client()
	app.typeSafeBaseURL = server.URL
	if err := typesafeintegration.UpdateAPIKey(config.RootPath(), "failure-generation"); err != nil {
		t.Fatal(err)
	}
	next := cfg
	next.Integrations.TypeSafe.Enabled = true
	next.Approval.Semantic.Enabled = true
	if err := app.ReloadConfig(next); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	workspace, err := app.Tools.Workspaces.Register(root)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := app.Tools.Call(context.Background(), "remember", map[string]any{
		"workspace_id": workspace.ID, "scope": "fallback", "key": "note", "note": "native memory result",
	}); err != nil || result.IsError {
		t.Fatalf("remember=%#v err=%v", result, err)
	}
	search, err := app.Tools.Call(context.Background(), "memory_search", map[string]any{
		"workspace_id": workspace.ID, "query": "native memory", "limit": 1,
	})
	if err != nil || search.IsError {
		t.Fatalf("fallback search=%#v err=%v", search, err)
	}
	searchResult, ok := search.StructuredContent.(tools.MemorySearchResult)
	if !ok || searchResult.Semantic == nil || searchResult.Semantic.Used || !searchResult.Semantic.Fallback || len(searchResult.Matches) != 1 {
		t.Fatalf("fallback search result=%#v", search.StructuredContent)
	}

	for _, name := range []string{"risk-rate-limited", "risk-low-confidence"} {
		target := filepath.Join(root, name)
		ctx := tools.WithCallSource(context.Background(), "tunnel")
		ctx = tools.WithApprovalCorrelation(ctx, "failure-"+name, "request-"+name)
		result, err := app.Tools.Call(ctx, "run_command", map[string]any{
			"workspace_id": workspace.ID,
			"command":      "touch " + filepath.Base(target),
		})
		if err != nil || !result.IsError || result.StructuredContent == nil {
			t.Fatalf("%s result=%#v err=%v", name, result, err)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("%s executed despite failed risk evidence: %v", name, err)
		}
	}
}

func TestTypeSafeRuntimeRejectsPartialProductionRegistration(t *testing.T) {
	cfg := isolateTypeSafeApp(t)
	cfg.Integrations.TypeSafe.Enabled = false
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	partial := typeSafeRuntimeCandidate{
		fingerprint: "partial",
		registration: semantic.ProviderRegistration{
			Provider: semantic.ProviderFunc(func(context.Context, semantic.Request) (semantic.Result, error) {
				return semantic.Result{}, nil
			}),
			Model: "partial",
		},
	}
	if err := app.commitTypeSafe(partial); !semantic.IsCategory(err, semantic.ErrorUnavailable) {
		t.Fatalf("partial TypeSafe registration err=%#v", err)
	}
	if health := app.Tools.Semantic.Health(); health.Available || health.LastErrorCategory != semantic.ErrorDisabled {
		t.Fatalf("partial registration changed runtime health=%#v", health)
	}
}
