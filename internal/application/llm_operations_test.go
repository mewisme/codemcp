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
		result: llm.Result{Model: "qwen3:8b", Text: "OK"},
		models: []llm.Model{{ID: "vendor/model-a", Name: "Model A"}},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher()
	if err := BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}

	const secret = "sk-or-v1-canonical-operation-secret"
	setResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderCredentialSet, Input: LLMProviderCredentialInput{ID: string(llm.OllamaID), APIKey: secret}})
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
	if !ok || status.ActiveProvider != llm.OllamaID || !status.Active.Configured || status.Active.Readiness != llm.ReadinessUnknown {
		t.Fatalf("status=%#v", statusResult.Value)
	}
	if backend.inferCalls.Load() != 0 || backend.discoverCalls.Load() != 0 {
		t.Fatalf("status performed network-like backend work: infer=%d discover=%d", backend.inferCalls.Load(), backend.discoverCalls.Load())
	}

	modelResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderModels, Input: LLMProviderModelsInput{ID: string(llm.OllamaID), Query: LLMModelQuery{Refresh: true}}})
	if err != nil {
		t.Fatal(err)
	}
	models, ok := modelResult.Value.(LLMModelCatalogResult)
	if !ok || !models.Refreshed || len(models.Models) != 1 || backend.discoverCalls.Load() != 1 {
		t.Fatalf("models=%#v discover_calls=%d", modelResult.Value, backend.discoverCalls.Load())
	}
	probeResult, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderProbe, Input: LLMProviderIDInput{ID: string(llm.OllamaID)}})
	if err != nil {
		t.Fatal(err)
	}
	probe, ok := probeResult.Value.(LLMProbeResult)
	if !ok || probe.Readiness != llm.ReadinessReady || backend.inferCalls.Load() != 1 {
		t.Fatalf("probe=%#v infer_calls=%d", probeResult.Value, backend.inferCalls.Load())
	}
	if backend.lastProvider != llm.OllamaID {
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
	if _, err := dispatcher.Dispatch(t.Context(), DispatchRequest{Operation: capability.LLMProviderSelect, Input: LLMProviderIDInput{ID: string(llm.OllamaID)}}); err != nil {
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
		result: llm.Result{Model: "qwen3:8b", Text: "OK"},
		models: []llm.Model{{ID: "remote/model"}},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	const secret = "sk-or-v1-doctor-must-never-render"
	if err := service.SetCredential(t.Context(), string(llm.OllamaID), secret); err != nil {
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

	if err := service.ClearCredential(t.Context(), string(llm.OllamaID)); err != nil {
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
	if result.Text != "result" || backend.inferCalls.Load() != 1 || backend.lastProvider != llm.OllamaID {
		t.Fatalf("result=%#v calls=%d provider=%q", result, backend.inferCalls.Load(), backend.lastProvider)
	}
}

func TestInactiveProvidersRemainFullyOperableWithoutChangingActiveSelection(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	backend := &llmBackendFixture{
		result: llm.Result{Model: "fixture", Text: "OK"},
		models: []llm.Model{{ID: "catalog/model-a", Name: "Model A"}},
	}
	service := NewLLMServiceWithBackend(root, backend)
	assertActive := func(want llm.ProviderID) {
		t.Helper()
		status, err := service.Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.ActiveProvider != want {
			t.Fatalf("active provider=%q want=%q", status.ActiveProvider, want)
		}
	}

	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ollama := providerResultByID(t, status.Providers, llm.OllamaID)
	if !ollama.Selected || ollama.BaseURL != llm.OllamaCloudBaseURL || ollama.AuthMode != llm.AuthBearer {
		t.Fatalf("fresh active Ollama=%#v", ollama)
	}

	if err := service.SetCredential(t.Context(), string(llm.OllamaID), "ollama-inactive-key"); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if _, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if backend.lastProvider != llm.OllamaID {
		t.Fatalf("inactive Ollama model discovery used provider=%q", backend.lastProvider)
	}
	assertActive(llm.OllamaID)
	if _, err := service.Probe(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	if backend.lastProvider != llm.OllamaID {
		t.Fatalf("inactive Ollama probe used provider=%q", backend.lastProvider)
	}
	assertActive(llm.OllamaID)

	local, err := service.SetOllamaMode(t.Context(), llm.OllamaModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.BaseURL != llm.OllamaLocalBaseURL || local.AuthMode != llm.AuthNone || local.Model != "qwen3:8b" {
		t.Fatalf("inactive local Ollama=%#v", local)
	}
	assertActive(llm.OllamaID)
	cloud, err := service.SetOllamaMode(t.Context(), llm.OllamaModeCloud)
	if err != nil {
		t.Fatal(err)
	}
	if cloud.BaseURL != llm.OllamaCloudBaseURL || cloud.AuthMode != llm.AuthBearer || cloud.Model != "qwen3:8b" {
		t.Fatalf("inactive cloud Ollama=%#v", cloud)
	}
	credential, err := service.CredentialResult(t.Context(), string(llm.OllamaID))
	if err != nil || !credential.Configured {
		t.Fatalf("Ollama credential after mode switches=%#v err=%v", credential, err)
	}
	if err := service.ClearCredential(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if err := service.SetCredential(t.Context(), string(llm.OllamaID), "ollama-inactive-key-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)

	if _, err := service.AddCustomProvider(t.Context(), "inactive-custom", CustomLLMProviderConfig{
		Name: "Inactive Custom", Protocol: llm.ProtocolOpenAI, BaseURL: "https://custom.example/v1",
		AuthMode: llm.AuthBearer, Discovery: llm.DiscoveryOpenAIModels,
	}); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if err := service.SetCredential(t.Context(), "inactive-custom", "custom-inactive-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetProviderModel(t.Context(), "inactive-custom", "custom/model-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModelCatalog(t.Context(), "inactive-custom", LLMModelQuery{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	if backend.lastProvider != "inactive-custom" {
		t.Fatalf("inactive custom discovery used provider=%q", backend.lastProvider)
	}
	if _, err := service.Probe(t.Context(), "inactive-custom"); err != nil {
		t.Fatal(err)
	}
	if backend.lastProvider != "inactive-custom" {
		t.Fatalf("inactive custom probe used provider=%q", backend.lastProvider)
	}
	if _, err := service.ConfigureCustomProvider(t.Context(), "inactive-custom", CustomLLMProviderConfig{
		Name: "Inactive Custom", Protocol: llm.ProtocolOpenAI, BaseURL: "https://custom-2.example/v1", Model: "custom/model-b",
		AuthMode: llm.AuthBearer, Discovery: llm.DiscoveryNone,
	}); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if err := service.ClearCredential(t.Context(), "inactive-custom"); err != nil {
		t.Fatal(err)
	}
	assertActive(llm.OllamaID)
	if err := service.SetCredential(t.Context(), "inactive-custom", "custom-inactive-key-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe(t.Context(), "inactive-custom"); err != nil {
		t.Fatal(err)
	}

	status, err = service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ollama = providerResultByID(t, status.Providers, llm.OllamaID)
	custom := providerResultByID(t, status.Providers, "inactive-custom")
	if !ollama.Selected || !ollama.Configured || ollama.Readiness != llm.ReadinessReady {
		t.Fatalf("active Ollama status=%#v", ollama)
	}
	if custom.Selected || !custom.Configured || custom.Readiness != llm.ReadinessReady {
		t.Fatalf("inactive custom readiness=%#v", custom)
	}

	if _, err := service.InferenceFacade().Infer(t.Context(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "use active"}}}); err != nil {
		t.Fatal(err)
	}
	if backend.lastProvider != llm.OllamaID {
		t.Fatalf("provider-omitted inference used %q instead of active provider", backend.lastProvider)
	}
}

func providerResultByID(t *testing.T, providers []LLMProviderResult, id llm.ProviderID) LLMProviderResult {
	t.Helper()
	for _, provider := range providers {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q not found in %#v", id, providers)
	return LLMProviderResult{}
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
