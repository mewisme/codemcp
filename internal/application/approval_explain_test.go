package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mewis.me/codemcp/internal/approval"
	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/controlguard"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

type approvalExplainInferenceFixture struct {
	mu       sync.Mutex
	requests []llm.Request
	calls    atomic.Int32
	infer    func(context.Context, llm.Request) (llm.Result, error)
}

func (fixture *approvalExplainInferenceFixture) Infer(ctx context.Context, request llm.Request) (llm.Result, error) {
	fixture.calls.Add(1)
	fixture.mu.Lock()
	fixture.requests = append(fixture.requests, request)
	fixture.mu.Unlock()
	if fixture.infer != nil {
		return fixture.infer(ctx, request)
	}
	return validApprovalExplainResult(), nil
}

func (fixture *approvalExplainInferenceFixture) lastRequest() llm.Request {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) == 0 {
		return llm.Request{}
	}
	return fixture.requests[len(fixture.requests)-1]
}

func validApprovalExplainResult() llm.Result {
	return llm.Result{
		ProviderID: llm.OllamaID,
		Model:      "qwen3:8b",
		Text:       `{"summary":"Runs curl against the supplied URL.","steps":["Invoke curl with an HTTP header."],"effects":["May make an outbound HTTP request."],"risk_notes":["The redacted credential value is unknown."],"unknowns":["Remote server behavior is unknown."]}`,
	}
}

func createApprovalExplainRequest(t *testing.T, manager *approval.Manager, targetTool, command string) approval.Request {
	t.Helper()
	const caller = "approval-explain-caller"
	challenge, _, err := manager.CreateChallenge(approval.ChallengeInput{
		CallerID: caller, RequestCorrelationID: "approval-explain-correlation", SessionHash: "session-hash", WorkspaceID: "ws_explain",
		Source: "tunnel", TargetTool: targetTool,
		Arguments:   map[string]any{"command": "ARGUMENT_COMMAND_MUST_NOT_BE_MODEL_INPUT", "title": "ARGUMENT_TITLE_MUST_NOT_BE_MODEL_INPUT"},
		GuardCode:   controlguard.CodeControlPlaneMutation,
		GuardReason: "AGENT_GUARD_REASON_MUST_NOT_BE_MODEL_INPUT",
		Title:       "AGENT_TITLE_MUST_NOT_BE_MODEL_INPUT",
		Command:     command,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := manager.CreateRequest(challenge.ID, caller, "ws_explain")
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func waitApprovalExplanationState(t *testing.T, service *ApprovalExplainService, requestID string, state ApprovalExplanationState) ApprovalExplanationResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, err := service.Read(t.Context(), requestID)
		if err == nil && value.State == state {
			return value
		}
		time.Sleep(5 * time.Millisecond)
	}
	value, err := service.Read(t.Context(), requestID)
	t.Fatalf("explanation state=%#v err=%v want=%s", value, err, state)
	return ApprovalExplanationResult{}
}

func TestApprovalExplainUsesOnlySanitizedExactCommandAndPreservesRequestAuthority(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain")
	const secret = "approval-explain-super-secret"
	request := createApprovalExplainRequest(t, manager, "run_command", `curl -H "Authorization: Bearer `+secret+`" https://example.test/path`)
	before, ok := manager.Get(request.ID)
	if !ok {
		t.Fatal("request missing before explanation")
	}
	fixture := &approvalExplainInferenceFixture{}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	result, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID})
	if err != nil || result.State != ApprovalExplanationPending {
		t.Fatalf("trigger=%#v err=%v", result, err)
	}
	ready := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationReady)
	if ready.Explanation == nil || ready.Explanation.ProviderID != llm.OllamaID || ready.Explanation.Model != "qwen3:8b" {
		t.Fatalf("ready=%#v", ready)
	}
	modelRequest := fixture.lastRequest()
	if len(modelRequest.Messages) != 1 {
		t.Fatalf("messages=%#v", modelRequest.Messages)
	}
	combined := modelRequest.Instructions + "\n" + modelRequest.Messages[0].Content
	for _, forbidden := range []string{secret, "AGENT_TITLE_MUST_NOT_BE_MODEL_INPUT", "AGENT_GUARD_REASON_MUST_NOT_BE_MODEL_INPUT", "ARGUMENT_COMMAND_MUST_NOT_BE_MODEL_INPUT", "ARGUMENT_TITLE_MUST_NOT_BE_MODEL_INPUT"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("model input leaked %q: %s", forbidden, combined)
		}
	}
	if !strings.Contains(modelRequest.Messages[0].Content, "curl") || !strings.Contains(modelRequest.Messages[0].Content, "<redacted>") {
		t.Fatalf("sanitized command missing structure/redaction: %q", modelRequest.Messages[0].Content)
	}
	for _, requiredProhibition := range []string{"do not recommend approving", "denying", "do not infer hidden intent"} {
		if !strings.Contains(strings.ToLower(modelRequest.Instructions), requiredProhibition) {
			t.Fatalf("prompt is missing reviewer-safety prohibition %q: %q", requiredProhibition, modelRequest.Instructions)
		}
	}
	after, ok := manager.Get(request.ID)
	if !ok {
		t.Fatal("request missing after explanation")
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("explanation mutated canonical approval request: before=%#v after=%#v", before, after)
	}
	if after.Status != before.Status || after.Digest != before.Digest || !after.ExpiresAt.Equal(before.ExpiresAt) || !after.RetryUntil.Equal(before.RetryUntil) || after.RuntimeSessionGrant != before.RuntimeSessionGrant || !after.GrantExpiresAt.Equal(before.GrantExpiresAt) {
		t.Fatalf("explanation mutated approval authority: before=%#v after=%#v", before, after)
	}

	events := manager.Events().Recent(20)
	foundReady := false
	for _, event := range events {
		if event.Name == approval.EventExplanationReady && event.RequestID == request.ID {
			foundReady = true
			encoded, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, forbidden := range []string{secret, "curl", ready.Explanation.Summary} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("lifecycle event leaked explanation input/output: %s", encoded)
				}
			}
		}
	}
	if !foundReady {
		t.Fatal("ready lifecycle event not published")
	}
}

func TestApprovalExplainAutoAndManualDeduplicateInflightGeneration(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-dedup")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture := &approvalExplainInferenceFixture{infer: func(context.Context, llm.Request) (llm.Result, error) {
		once.Do(func() { close(started) })
		<-release
		return validApprovalExplainResult(), nil
	}}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainAuto })
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()

	requestCreated := make(chan approval.Request, 1)
	go func() {
		requestCreated <- createApprovalExplainRequest(t, manager, "start_process", "go test ./...")
	}()
	var request approval.Request
	select {
	case request = <-requestCreated:
	case <-time.After(time.Second):
		t.Fatal("auto explanation delayed approval request creation")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("auto explanation did not start")
	}
	manual, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID})
	if err != nil || manual.State != ApprovalExplanationPending {
		t.Fatalf("manual dedup trigger=%#v err=%v", manual, err)
	}
	if fixture.calls.Load() != 1 {
		t.Fatalf("inflight calls=%d want=1", fixture.calls.Load())
	}
	close(release)
	ready := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationReady)
	if fixture.calls.Load() != 1 {
		t.Fatalf("dedup calls=%d want=1", fixture.calls.Load())
	}
	after, ok := manager.Get(request.ID)
	if !ok || !after.ExpiresAt.Equal(request.ExpiresAt) || ready.UpdatedAt.After(request.ExpiresAt) {
		t.Fatalf("auto explanation altered/bypassed request TTL: before=%s after=%s explanation=%s", request.ExpiresAt, after.ExpiresAt, ready.UpdatedAt)
	}
}

func TestApprovalExplainFailureRequiresExplicitRetryAndReviewRemainsUsable(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-retry")
	request := createApprovalExplainRequest(t, manager, "run_command", "git status")
	fixture := &approvalExplainInferenceFixture{}
	fixture.infer = func(context.Context, llm.Request) (llm.Result, error) {
		if fixture.calls.Load() == 1 {
			return llm.Result{ProviderID: llm.OllamaID, Model: "qwen3:8b", Text: `not json`}, nil
		}
		return validApprovalExplainResult(), nil
	}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	if _, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	failed := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationFailed)
	if failed.Attempt != 1 || failed.Failure == "" {
		t.Fatalf("failed=%#v", failed)
	}
	noRetry, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID})
	if err != nil || noRetry.State != ApprovalExplanationFailed || fixture.calls.Load() != 1 {
		t.Fatalf("implicit retry=%#v err=%v calls=%d", noRetry, err, fixture.calls.Load())
	}
	retrying, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID, Retry: true})
	if err != nil || retrying.State != ApprovalExplanationPending || retrying.Attempt != 2 {
		t.Fatalf("retry=%#v err=%v", retrying, err)
	}
	ready := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationReady)
	if ready.Attempt != 2 || fixture.calls.Load() != 2 {
		t.Fatalf("ready=%#v calls=%d", ready, fixture.calls.Load())
	}
	resolved, err := manager.Deny(request.ID, "reviewer", "review remains independent")
	if err != nil || resolved.Status != approval.StatusDenied {
		t.Fatalf("deny after explanation=%#v err=%v", resolved, err)
	}
}

func TestApprovalExplainTerminalRequestRejectsLateAuthoritativeResult(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-terminal")
	request := createApprovalExplainRequest(t, manager, "run_command", "go test ./internal/approval")
	started := make(chan struct{})
	release := make(chan struct{})
	fixture := &approvalExplainInferenceFixture{infer: func(context.Context, llm.Request) (llm.Result, error) {
		close(started)
		<-release // deliberately ignore cancellation to verify terminal revalidation
		return validApprovalExplainResult(), nil
	}}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	if _, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := manager.Deny(request.ID, "reviewer", "terminal while explanation runs"); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, err := service.Read(t.Context(), request.ID)
		if err == nil && value.State != ApprovalExplanationPending {
			if value.State == ApprovalExplanationReady || value.State == ApprovalExplanationFailed {
				t.Fatalf("late result became authoritative after terminal state: %#v", value)
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, event := range manager.Events().Recent(20) {
		if event.RequestID == request.ID && (event.Name == approval.EventExplanationReady || event.Name == approval.EventExplanationFailed) {
			t.Fatalf("late terminal explanation event published: %#v", event)
		}
	}
}

func TestApprovalExplainCancelledRequestRejectsLateAuthoritativeResult(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-cancelled")
	request := createApprovalExplainRequest(t, manager, "start_process", "go test ./internal/application")
	started := make(chan struct{})
	release := make(chan struct{})
	fixture := &approvalExplainInferenceFixture{infer: func(context.Context, llm.Request) (llm.Result, error) {
		close(started)
		<-release
		return validApprovalExplainResult(), nil
	}}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	if err := service.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	if _, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := manager.Cancel(request.ID, "runtime", "request cancelled"); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, err := service.Read(t.Context(), request.ID)
		if err == nil && value.State == ApprovalExplanationNone {
			break
		}
		if err == nil && (value.State == ApprovalExplanationReady || value.State == ApprovalExplanationFailed) {
			t.Fatalf("late cancelled explanation became authoritative: %#v", value)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, event := range manager.Events().Recent(20) {
		if event.RequestID == request.ID && (event.Name == approval.EventExplanationReady || event.Name == approval.EventExplanationFailed) {
			t.Fatalf("late cancelled explanation event published: %#v", event)
		}
	}
}

func TestApprovalExplainRejectsUnusableSanitizedCommandWithoutInference(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-redaction")
	request := createApprovalExplainRequest(t, manager, "run_command", "mcp_abcdefghijklmnopqrstuvwxyz123456")
	fixture := &approvalExplainInferenceFixture{}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	if _, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID}); err != nil {
		t.Fatal(err)
	}
	failed := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationFailed)
	if failed.Failure == "" || fixture.calls.Load() != 0 {
		t.Fatalf("failed=%#v calls=%d", failed, fixture.calls.Load())
	}
}

func TestApprovalExplainDispatcherContracts(t *testing.T) {
	manager := approval.NewManager("instance-approval-explain-dispatcher")
	request := createApprovalExplainRequest(t, manager, "run_command", "git status")
	fixture := &approvalExplainInferenceFixture{}
	service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
	dispatcher := NewDispatcher()
	if err := BindApprovalExplainOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	started, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.RequestExplain, Input: ApprovalExplainInput{ID: request.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if started.Metadata.Authorization != capability.AuthorizationReviewer || started.Metadata.Audience != capability.AudienceReviewer {
		t.Fatalf("request.explain metadata=%#v", started.Metadata)
	}
	_ = waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationReady)
	view, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.RequestExplanationView, Input: ApprovalExplanationReadInput{ID: request.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := view.Value.(ApprovalExplanationResult); !ok || value.State != ApprovalExplanationReady {
		t.Fatalf("view=%#v", view.Value)
	}
}

type approvalExplainBackend struct {
	mu       sync.Mutex
	inferFn  func(context.Context, llm.Provider, llm.Request) (llm.Result, error)
	calls    int
	requests []llm.Request
}

func (backend *approvalExplainBackend) Infer(ctx context.Context, provider llm.Provider, request llm.Request) (llm.Result, error) {
	backend.mu.Lock()
	backend.calls++
	backend.requests = append(backend.requests, request)
	fn := backend.inferFn
	backend.mu.Unlock()
	if fn != nil {
		return fn(ctx, provider, request)
	}
	return llm.Result{ProviderID: provider.ID, Model: provider.Model, Text: "OK"}, nil
}

func (backend *approvalExplainBackend) DiscoverModels(context.Context, llm.Provider) ([]llm.Model, error) {
	return nil, nil
}

func (backend *approvalExplainBackend) callCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.calls
}

func TestApprovalExplainSettingEnableIsAtomicAndRequiresConfiguredSuccessfulProbe(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	backend := &approvalExplainBackend{}
	llmService := NewLLMServiceWithBackend(root, backend)
	settings := NewSettingService(llmService)

	_, err := settings.Apply(t.Context(), []SettingChange{{Key: "approval.explain.mode", Value: "manual"}})
	if err == nil {
		t.Fatal("Explain enabled without required Ollama credential")
	}
	cfg, err := LoadConfig(t.Context())
	if err != nil || cfg.Approval.Explain.Mode != config.ApprovalExplainOff {
		t.Fatalf("mode persisted after failed configuration gate: mode=%q err=%v", cfg.Approval.Explain.Mode, err)
	}
	if backend.callCount() != 0 {
		t.Fatalf("probe ran with incomplete configuration: calls=%d", backend.callCount())
	}

	if err := llmService.SetCredential(t.Context(), string(llm.OllamaID), "sk-or-v1-enable-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := llmService.SetProviderModel(t.Context(), string(llm.OllamaID), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	backend.inferFn = func(context.Context, llm.Provider, llm.Request) (llm.Result, error) {
		return llm.Result{}, llm.NewError(llm.ErrorUnauthorized, "", "secret response details")
	}
	_, err = settings.Apply(t.Context(), []SettingChange{{Key: "approval.explain.mode", Value: "manual"}})
	if err == nil {
		t.Fatal("Explain enabled despite failed explicit probe")
	}
	cfg, err = LoadConfig(t.Context())
	if err != nil || cfg.Approval.Explain.Mode != config.ApprovalExplainOff {
		t.Fatalf("mode persisted after failed probe: mode=%q err=%v", cfg.Approval.Explain.Mode, err)
	}

	backend.inferFn = nil
	result, err := settings.Apply(t.Context(), []SettingChange{{Key: "approval.explain.mode", Value: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Approval.Explain.Mode != config.ApprovalExplainManual {
		t.Fatalf("mode=%q", result.Config.Approval.Explain.Mode)
	}
	persisted, err := json.Marshal(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Return a short acknowledgement.", "Respond with OK.", "qwen3:8b", "ApprovalExplanation", "risk_notes"} {
		if strings.Contains(string(persisted), forbidden) {
			t.Fatalf("successful enable persisted probe/prompt/explanation material %q: %s", forbidden, persisted)
		}
	}
	status := NewApprovalExplainService(approval.NewManager("instance-status"), llmService, func() config.ApprovalExplainMode { return result.Config.Approval.Explain.Mode })
	availability, err := status.Status(t.Context())
	if err != nil || !availability.Available || availability.Readiness != llm.ReadinessReady {
		t.Fatalf("availability=%#v err=%v", availability, err)
	}
}

func TestApprovalExplainStatusBecomesUnavailableAfterProviderSwitchOrFailureWithoutChangingMode(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	backend := &approvalExplainBackend{}
	llmService := NewLLMServiceWithBackend(root, backend)
	if err := llmService.SetCredential(t.Context(), string(llm.OllamaID), "sk-or-v1-status-test"); err != nil {
		t.Fatal(err)
	}
	if _, err := llmService.SetProviderModel(t.Context(), string(llm.OllamaID), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	if _, err := llmService.Probe(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	mode := config.ApprovalExplainManual
	service := NewApprovalExplainService(approval.NewManager("instance-status-switch"), llmService, func() config.ApprovalExplainMode { return mode })
	status, err := service.Status(t.Context())
	if err != nil || !status.Available {
		t.Fatalf("initial status=%#v err=%v", status, err)
	}

	custom, err := llmService.AddCustomProvider(t.Context(), "explain-local", CustomLLMProviderConfig{
		Protocol: llm.ProtocolOpenAI, BaseURL: "http://127.0.0.1:9999/v1", Model: "local-model", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := llmService.SelectProvider(t.Context(), string(custom.ID)); err != nil {
		t.Fatal(err)
	}
	status, err = service.Status(t.Context())
	if err != nil || status.Available || status.Mode != config.ApprovalExplainManual || status.Readiness != llm.ReadinessUnknown {
		t.Fatalf("switched status=%#v err=%v", status, err)
	}

	if _, err := llmService.SelectProvider(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	backend.inferFn = func(context.Context, llm.Provider, llm.Request) (llm.Result, error) {
		return llm.Result{}, llm.NewError(llm.ErrorTimeout, "", "provider timed out")
	}
	_, _ = llmService.InferenceFacade().Infer(t.Context(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "health"}}})
	status, err = service.Status(t.Context())
	if err != nil || status.Available || status.Mode != config.ApprovalExplainManual || status.Readiness != llm.ReadinessUnavailable {
		t.Fatalf("failed-provider status=%#v err=%v", status, err)
	}
}

func TestParseApprovalExplanationRejectsMalformedUnknownAndOversizedOutput(t *testing.T) {
	base := validApprovalExplainResult()
	for name, text := range map[string]string{
		"malformed": `{`,
		"unknown":   `{"summary":"x","steps":[],"effects":[],"risk_notes":[],"unknowns":[],"decision":"approve"}`,
		"oversized": `{"summary":"` + strings.Repeat("x", maxApprovalExplainSummaryRunes+1) + `","steps":[],"effects":[],"risk_notes":[],"unknowns":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Text = text
			if _, err := parseApprovalExplanation(value, time.Now()); err == nil {
				t.Fatal("invalid output accepted")
			}
		})
	}
}

func TestApprovalExplanationStateIsNotPartOfApprovalRequestAuthority(t *testing.T) {
	typeOf := reflect.TypeOf(approval.Request{})
	for _, forbidden := range []string{"Explanation", "ExplanationState", "ExplanationAttempt", "ProviderID", "Model"} {
		if _, ok := typeOf.FieldByName(forbidden); ok {
			t.Fatalf("approval.Request unexpectedly owns explanation field %s", forbidden)
		}
	}
}

func TestApprovalExplainCommandRedactsSensitiveAssignmentsBeforeInference(t *testing.T) {
	source := approval.ExplanationSource{TargetTool: "run_command", Command: `OPENROUTER_API_KEY=sk-or-secret cm run --token=another-secret safe-arg`}
	safe, err := approvalExplainCommand(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sk-or-secret", "another-secret"} {
		if strings.Contains(safe, forbidden) {
			t.Fatalf("sanitized command leaked %q: %q", forbidden, safe)
		}
	}
	if !strings.Contains(safe, "OPENROUTER_API_KEY=<redacted>") || !strings.Contains(safe, "--token=<redacted>") || !strings.Contains(safe, "safe-arg") {
		t.Fatalf("sanitized command lost structure: %q", safe)
	}
}

func TestApprovalExplainProviderFailuresDoNotExposeRawErrorsOrBreakReview(t *testing.T) {
	for name, category := range map[string]llm.ErrorCategory{
		"timeout": llm.ErrorTimeout,
		"auth":    llm.ErrorUnauthorized,
		"rate":    llm.ErrorRateLimited,
	} {
		t.Run(name, func(t *testing.T) {
			manager := approval.NewManager("instance-approval-explain-provider-" + name)
			request := createApprovalExplainRequest(t, manager, "run_command", "git status")
			const sensitive = "provider-secret-error-payload"
			fixture := &approvalExplainInferenceFixture{infer: func(context.Context, llm.Request) (llm.Result, error) {
				return llm.Result{}, llm.NewError(category, "", sensitive)
			}}
			service := newApprovalExplainService(manager, nil, fixture, func() config.ApprovalExplainMode { return config.ApprovalExplainManual })
			if _, err := service.Trigger(t.Context(), ApprovalExplainInput{ID: request.ID}); err != nil {
				t.Fatal(err)
			}
			failed := waitApprovalExplanationState(t, service, request.ID, ApprovalExplanationFailed)
			if strings.Contains(failed.Failure, sensitive) || failed.Failure == "" {
				t.Fatalf("unsafe failure=%q", failed.Failure)
			}
			approved, err := manager.Approve(request.ID, "reviewer", "review still works")
			if err != nil || approved.Status != approval.StatusApproved {
				t.Fatalf("approve=%#v err=%v", approved, err)
			}
		})
	}
}
