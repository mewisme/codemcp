package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
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

type autoModelBackend struct {
	models        []llm.Model
	fail          map[string]error
	inferModels   []string
	discoverCalls int
}

type modelAccessBackend struct {
	mu            sync.Mutex
	models        []llm.Model
	fail          map[string]error
	inferModels   []string
	discoverCalls int
}

func (backend *modelAccessBackend) Infer(_ context.Context, provider llm.Provider, _ llm.Request) (llm.Result, error) {
	backend.mu.Lock()
	backend.inferModels = append(backend.inferModels, provider.Model)
	err := backend.fail[provider.Model]
	backend.mu.Unlock()
	if err != nil {
		return llm.Result{}, err
	}
	return llm.Result{ProviderID: provider.ID, Model: provider.Model, Text: "OK"}, nil
}

func (backend *modelAccessBackend) DiscoverModels(context.Context, llm.Provider) ([]llm.Model, error) {
	backend.mu.Lock()
	backend.discoverCalls++
	backend.mu.Unlock()
	return cloneLLMModels(backend.models), nil
}

func (backend *modelAccessBackend) resetInferModels() {
	backend.mu.Lock()
	backend.inferModels = nil
	backend.mu.Unlock()
}

func (backend *modelAccessBackend) snapshot() ([]string, int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]string(nil), backend.inferModels...), backend.discoverCalls
}

func (backend *autoModelBackend) Infer(_ context.Context, provider llm.Provider, _ llm.Request) (llm.Result, error) {
	backend.inferModels = append(backend.inferModels, provider.Model)
	if err := backend.fail[provider.Model]; err != nil {
		return llm.Result{}, err
	}
	return llm.Result{ProviderID: provider.ID, Model: provider.Model, Text: "OK"}, nil
}

func (backend *autoModelBackend) DiscoverModels(context.Context, llm.Provider) ([]llm.Model, error) {
	backend.discoverCalls++
	return cloneLLMModels(backend.models), nil
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

func TestOllamaAutoModelDiscoversFailsOverAndSticksToSuccessfulModel(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &autoModelBackend{
		models: []llm.Model{{ID: "model-a"}, {ID: "model-b"}, {ID: "model-c"}},
		fail: map[string]error{
			"model-a": llm.NewError(llm.ErrorInvalidResponse, "response", "model-a returned malformed output"),
		},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), llm.OllamaAutoModel); err != nil {
		t.Fatal(err)
	}

	request := llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}}
	result, err := service.InferenceFacade().Infer(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "model-b" || !reflect.DeepEqual(backend.inferModels, []string{"model-a", "model-b"}) {
		t.Fatalf("first auto inference result=%#v models=%v", result, backend.inferModels)
	}
	if backend.discoverCalls != 1 {
		t.Fatalf("discover calls=%d want=1", backend.discoverCalls)
	}

	backend.inferModels = nil
	result, err = service.InferenceFacade().Infer(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "model-b" || !reflect.DeepEqual(backend.inferModels, []string{"model-b"}) {
		t.Fatalf("sticky auto inference result=%#v models=%v", result, backend.inferModels)
	}
	if backend.discoverCalls != 1 {
		t.Fatalf("cached discovery calls=%d want=1", backend.discoverCalls)
	}

	backend.inferModels = nil
	if err := service.ProbeProvider(t.Context(), string(llm.OllamaID)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.inferModels, []string{"model-b"}) {
		t.Fatalf("auto probe models=%v", backend.inferModels)
	}
}

func TestOllamaAutoModelDoesNotFailOverProviderWideErrors(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &autoModelBackend{
		models: []llm.Model{{ID: "model-a"}, {ID: "model-b"}},
		fail: map[string]error{
			"model-a": llm.NewError(llm.ErrorUnauthorized, "", "invalid credential"),
		},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), llm.OllamaAutoModel); err != nil {
		t.Fatal(err)
	}
	_, err := service.InferenceFacade().Infer(t.Context(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}})
	if !llm.IsCategory(err, llm.ErrorUnauthorized) {
		t.Fatalf("auto unauthorized err=%v", err)
	}
	if !reflect.DeepEqual(backend.inferModels, []string{"model-a"}) {
		t.Fatalf("provider-wide error should not fail over: %v", backend.inferModels)
	}
}

func TestOllamaModelAccessCheckUsesRealInferenceResultsAndCachesThem(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &modelAccessBackend{
		models: []llm.Model{{ID: "model-a"}, {ID: "model-b"}, {ID: "model-c"}},
		fail: map[string]error{
			"model-a": llm.NewError(llm.ErrorProvider, "", "provider returned HTTP 402"),
			"model-c": llm.NewError(llm.ErrorTimeout, "", "provider request timed out"),
		},
	}
	service := NewLLMServiceWithBackend(root, backend)

	page, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{CheckAccess: true, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !page.AccessChecked || page.AccessAvailable != 1 || page.AccessUnavailable != 1 || page.AccessUnknown != 1 {
		t.Fatalf("access summary=%#v", page)
	}
	if got := page.ModelAccess["model-a"]; got.State != LLMModelAccessUnavailable || got.ErrorCategory != llm.ErrorProvider || !strings.Contains(got.Reason, "402") {
		t.Fatalf("model-a access=%#v", got)
	}
	if got := page.ModelAccess["model-b"]; got.State != LLMModelAccessAvailable {
		t.Fatalf("model-b access=%#v", got)
	}
	if got := page.ModelAccess["model-c"]; got.State != LLMModelAccessUnknown || got.ErrorCategory != llm.ErrorTimeout {
		t.Fatalf("model-c access=%#v", got)
	}
	firstModels, firstDiscoveries := backend.snapshot()
	if len(firstModels) != 3 || firstDiscoveries != 1 {
		t.Fatalf("first access scan models=%v discoveries=%d", firstModels, firstDiscoveries)
	}

	if _, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{CheckAccess: true, All: true}); err != nil {
		t.Fatal(err)
	}
	secondModels, secondDiscoveries := backend.snapshot()
	if len(secondModels) != 3 || secondDiscoveries != 1 {
		t.Fatalf("cached access scan models=%v discoveries=%d", secondModels, secondDiscoveries)
	}

	if _, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{CheckAccess: true, Refresh: true, All: true}); err != nil {
		t.Fatal(err)
	}
	thirdModels, thirdDiscoveries := backend.snapshot()
	if len(thirdModels) != 6 || thirdDiscoveries != 2 {
		t.Fatalf("refreshed access scan models=%v discoveries=%d", thirdModels, thirdDiscoveries)
	}
}

func TestOllamaAutoUsesAccessCheckToSkipUnavailableModels(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &modelAccessBackend{
		models: []llm.Model{{ID: "paid-model"}, {ID: "free-model"}, {ID: "backup-model"}},
		fail: map[string]error{
			"paid-model": llm.NewError(llm.ErrorProvider, "", "provider returned HTTP 402"),
		},
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.SetProviderModel(t.Context(), string(llm.OllamaID), llm.OllamaAutoModel); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ModelCatalog(t.Context(), string(llm.OllamaID), LLMModelQuery{CheckAccess: true, All: true}); err != nil {
		t.Fatal(err)
	}
	backend.resetInferModels()

	result, err := service.InferenceFacade().Infer(t.Context(), llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	models, _ := backend.snapshot()
	if result.Model != "free-model" || !reflect.DeepEqual(models, []string{"free-model"}) {
		t.Fatalf("auto result=%#v attempted=%v", result, models)
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
