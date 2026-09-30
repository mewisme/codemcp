package application

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/doctor"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type llmBackendFixture struct {
	inferCalls    atomic.Int32
	discoverCalls atomic.Int32
	lastProvider  llm.ProviderID
	result        llm.Result
	models        []llm.Model
	inferErr      error
	discoverErr   error
}

func (fixture *llmBackendFixture) Infer(_ context.Context, provider llm.Provider, _ llm.Request) (llm.Result, error) {
	fixture.inferCalls.Add(1)
	fixture.lastProvider = provider.ID
	return fixture.result, fixture.inferErr
}

func (fixture *llmBackendFixture) DiscoverModels(_ context.Context, provider llm.Provider) ([]llm.Model, error) {
	fixture.discoverCalls.Add(1)
	fixture.lastProvider = provider.ID
	return cloneLLMModels(fixture.models), fixture.discoverErr
}

func TestCanonicalLLMOperationsBindStableResultsAndProtectedCredentials(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	backend := &llmBackendFixture{
		result: llm.Result{Model: "openrouter/free", Text: "OK"},
		models: []llm.Model{{ID: "vendor/model-a", Name: "Model A"}},
	}
	service := NewLLMServiceWithBackend(root, backend)
	dispatcher := NewDispatcher()
	if err := BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}

	const secret = "sk-or-v1-canonical-operation-secret"
	setResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderCredentialSet, Input: LLMProviderCredentialInput{ID: string(llm.OpenRouterID), APIKey: secret}})
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := setResult.Value.(LLMCredentialResult)
	if !ok || !credential.Configured || credential.Preview != tracepkg.MaskSecret(secret, true) || strings.Contains(credential.Preview, secret) {
		t.Fatalf("credential result=%#v", setResult.Value)
	}
	encoded, err := json.Marshal(setResult.Value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("dispatcher result leaked raw credential: %s", encoded)
	}
	if setResult.Metadata.ID != capability.LLMProviderCredentialSet || setResult.Metadata.Risk != capability.RiskSensitive {
		t.Fatalf("credential operation spec=%#v", setResult.Metadata)
	}

	statusResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMStatus})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := statusResult.Value.(LLMStatusResult)
	if !ok || status.ActiveProvider != llm.OpenRouterID || !status.Active.Configured || status.Active.Readiness != llm.ReadinessUnknown {
		t.Fatalf("status=%#v", statusResult.Value)
	}
	if backend.inferCalls.Load() != 0 || backend.discoverCalls.Load() != 0 {
		t.Fatalf("status performed network-like backend work: infer=%d discover=%d", backend.inferCalls.Load(), backend.discoverCalls.Load())
	}

	modelResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderModels, Input: LLMProviderModelsInput{ID: string(llm.OpenRouterID), Refresh: true}})
	if err != nil {
		t.Fatal(err)
	}
	models, ok := modelResult.Value.(LLMModelCatalogResult)
	if !ok || !models.Refreshed || len(models.Models) != 1 || backend.discoverCalls.Load() != 1 {
		t.Fatalf("models=%#v discover_calls=%d", modelResult.Value, backend.discoverCalls.Load())
	}
	probeResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderProbe, Input: LLMProviderIDInput{ID: string(llm.OpenRouterID)}})
	if err != nil {
		t.Fatal(err)
	}
	probe, ok := probeResult.Value.(LLMProbeResult)
	if !ok || probe.Readiness != llm.ReadinessReady || backend.inferCalls.Load() != 1 {
		t.Fatalf("probe=%#v infer_calls=%d", probeResult.Value, backend.inferCalls.Load())
	}
	if backend.lastProvider != llm.OpenRouterID {
		t.Fatalf("backend provider=%q", backend.lastProvider)
	}
}

func TestLLMDispatcherOwnsCustomProviderMutationAndTypedErrors(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMServiceWithBackend(root, &llmBackendFixture{})
	dispatcher := NewDispatcher()
	if err := BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	add, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderAdd, Input: LLMProviderWriteInput{
		ID: "custom-dispatch",
		Config: CustomLLMProviderConfig{
			Protocol: llm.ProtocolOpenAI, BaseURL: "https://custom.example/v1", Model: "model-a",
			AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := add.Value.(LLMProviderResult)
	if !ok || provider.ID != "custom-dispatch" || provider.Selected {
		t.Fatalf("add result=%#v", add.Value)
	}
	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderSelect, Input: LLMProviderIDInput{ID: "custom-dispatch"}}); err != nil {
		t.Fatal(err)
	}
	_, err = dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderRemove, Input: LLMProviderIDInput{ID: "custom-dispatch"}})
	var operationErr *OperationError
	if !errorsAsOperation(err, &operationErr) || operationErr.Code != ErrorConflict {
		t.Fatalf("active remove err=%v", err)
	}
	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderSelect, Input: LLMProviderIDInput{ID: string(llm.OpenRouterID)}}); err != nil {
		t.Fatal(err)
	}
	removed, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderRemove, Input: LLMProviderIDInput{ID: "custom-dispatch"}})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := removed.Value.(LLMProviderRemoveResult)
	if !ok || !value.Removed || value.ProviderID != "custom-dispatch" {
		t.Fatalf("remove result=%#v", removed.Value)
	}
}

func TestLLMDoctorIsLocalReadOnlyAndSecretSafe(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	backend := &llmBackendFixture{
		result: llm.Result{Model: "openrouter/free", Text: "OK"},
		models: []llm.Model{{ID: "remote/model"}},
	}
	service := NewLLMServiceWithBackend(root, backend)
	const secret = "sk-or-v1-doctor-must-never-render"
	if err := service.SetCredential(t.Context(), string(llm.OpenRouterID), secret); err != nil {
		t.Fatal(err)
	}

	var provider doctor.Provider
	for _, candidate := range defaultDoctorProviders(DoctorDependencies{LLM: service}) {
		if candidate.Spec().ID == doctor.ComponentLLMProvider {
			provider = candidate
			break
		}
	}
	if provider == nil {
		t.Fatal("LLM doctor provider is not registered")
	}
	component, err := provider.Diagnose(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if component.State != doctor.StateHealthy || component.Probe != "" {
		// Provider metadata is attached by the collector; direct provider output stays domain-only.
		if component.State != doctor.StateHealthy {
			t.Fatalf("doctor component=%#v", component)
		}
	}
	if backend.inferCalls.Load() != 0 || backend.discoverCalls.Load() != 0 {
		t.Fatalf("doctor invoked remote backend: infer=%d discover=%d", backend.inferCalls.Load(), backend.discoverCalls.Load())
	}
	encoded, err := json.Marshal(component)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), tracepkg.MaskSecret(secret, true)) {
		t.Fatalf("doctor leaked credential material: %s", encoded)
	}

	if err := service.ClearCredential(t.Context(), string(llm.OpenRouterID)); err != nil {
		t.Fatal(err)
	}
	component, err = provider.Diagnose(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if component.State != doctor.StateDegraded || backend.inferCalls.Load() != 0 || backend.discoverCalls.Load() != 0 {
		t.Fatalf("degraded doctor component=%#v infer=%d discover=%d", component, backend.inferCalls.Load(), backend.discoverCalls.Load())
	}
}

func TestLLMInferenceFacadeUsesInjectedBackendAndActiveProvider(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &llmBackendFixture{result: llm.Result{Model: "fixture", Text: "result"}}
	service := NewLLMServiceWithBackend(root, backend)
	result, err := service.InferenceFacade().Infer(t.Context(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "result" || backend.inferCalls.Load() != 1 || backend.lastProvider != llm.OpenRouterID {
		t.Fatalf("result=%#v calls=%d provider=%q", result, backend.inferCalls.Load(), backend.lastProvider)
	}
}

func errorsAsOperation(err error, target **OperationError) bool {
	if err == nil {
		return false
	}
	value, ok := err.(*OperationError)
	if !ok {
		return false
	}
	*target = value
	return true
}
