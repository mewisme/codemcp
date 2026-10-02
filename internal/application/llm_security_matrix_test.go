package application

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"go.mewis.me/codemcp/internal/capability"
	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

type llmSecurityMatrixBackend struct {
	models      []llm.Model
	inferErr    error
	discoverErr error
}

func (backend *llmSecurityMatrixBackend) Infer(_ context.Context, provider llm.Provider, _ llm.Request) (llm.Result, error) {
	if backend.inferErr != nil {
		return llm.Result{}, backend.inferErr
	}
	return llm.Result{ProviderID: provider.ID, Model: provider.Model, Text: "OK"}, nil
}

func (backend *llmSecurityMatrixBackend) DiscoverModels(_ context.Context, _ llm.Provider) ([]llm.Model, error) {
	if backend.discoverErr != nil {
		return nil, backend.discoverErr
	}
	return cloneLLMModels(backend.models), nil
}

func TestLLMConcurrentMutationMatrixPreservesCoreProvidersAndSingleActiveSelection(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &llmSecurityMatrixBackend{models: []llm.Model{{ID: "fixture/model-a"}, {ID: "fixture/model-b"}}}
	bootstrap := NewLLMServiceWithBackend(root, backend)
	for _, item := range []struct {
		id   string
		name string
	}{
		{id: "selected-custom", name: "Selected Custom"},
		{id: "configured-custom", name: "Configured Custom"},
		{id: "removal-victim", name: "Removal Victim"},
	} {
		if _, err := bootstrap.AddCustomProvider(t.Context(), item.id, CustomLLMProviderConfig{
			Name: item.name, Protocol: llm.ProtocolOpenAI, BaseURL: "https://" + item.id + ".example/v1",
			Model: "fixture/model-a", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
		}); err != nil {
			t.Fatal(err)
		}
	}

	services := make([]*LLMService, 8)
	for index := range services {
		services[index] = NewLLMServiceWithBackend(root, backend)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 128)
	record := func(err error) {
		if err != nil {
			select {
			case errs <- err:
			default:
			}
		}
	}

	selectionTargets := []string{string(llm.OllamaID), string(llm.OllamaID), "selected-custom", "configured-custom"}
	for worker := 0; worker < 6; worker++ {
		service := services[worker]
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 40; iteration++ {
				_, err := service.SelectProvider(t.Context(), selectionTargets[(worker+iteration)%len(selectionTargets)])
				record(err)
			}
		}()
	}

	for worker := 0; worker < 3; worker++ {
		service := services[worker+1]
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 30; iteration++ {
				_, err := service.ConfigureCustomProvider(t.Context(), "configured-custom", CustomLLMProviderConfig{
					Name: "Configured Custom", Protocol: llm.ProtocolOpenAI,
					BaseURL: fmt.Sprintf("https://configured-%d-%d.example/v1", worker, iteration),
					Model:   fmt.Sprintf("fixture/model-%d", iteration%2), AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
				})
				record(err)
			}
		}()
	}

	for worker := 0; worker < 3; worker++ {
		service := services[worker+3]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 40; iteration++ {
				_, err := service.ModelCatalog(t.Context(), "configured-custom", LLMModelQuery{Refresh: iteration%7 == 0, Limit: 1})
				record(err)
			}
		}()
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		for iteration := 0; iteration < 40; iteration++ {
			_, err := services[6].ModelCatalog(t.Context(), "removal-victim", LLMModelQuery{Refresh: true, Limit: 1})
			if err != nil && !llm.IsCategory(err, llm.ErrorProviderNotFound) {
				record(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		record(services[7].RemoveProvider(t.Context(), "removal-victim"))
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent LLM mutation failed: %v", err)
	}

	catalog, err := llm.NewStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := llm.NormalizeCatalog(catalog); err != nil {
		t.Fatalf("final catalog violates canonical invariants: %v", err)
	}
	counts := map[llm.ProviderID]int{}
	activeExists := false
	for _, provider := range catalog.Providers {
		counts[provider.ID]++
		if provider.ID == catalog.ActiveProvider {
			activeExists = true
		}
	}
	if counts[llm.OllamaID] != 1 {
		t.Fatalf("core provider permanence violated: counts=%#v catalog=%#v", counts, catalog)
	}
	if !activeExists {
		t.Fatalf("active provider %q is not registered: %#v", catalog.ActiveProvider, catalog)
	}
	if counts["removal-victim"] != 0 {
		t.Fatalf("inactive custom provider removal was lost: %#v", catalog)
	}

	status, err := NewLLMServiceWithBackend(root, backend).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selectedCount := 0
	for _, provider := range status.Providers {
		if provider.Selected {
			selectedCount++
			if provider.ID != catalog.ActiveProvider {
				t.Fatalf("surface selection %q diverged from persisted active provider %q", provider.ID, catalog.ActiveProvider)
			}
		}
	}
	if selectedCount != 1 {
		t.Fatalf("selected providers=%d want exactly one: %#v", selectedCount, status.Providers)
	}
}

func TestLLMProviderOutageAndCatalogRefreshFailureNeverMutatePersistedSelectionOrConfiguration(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	backend := &llmSecurityMatrixBackend{
		inferErr:    llm.NewError(llm.ErrorUnavailable, "", "fixture inference outage"),
		discoverErr: llm.NewError(llm.ErrorTransport, "", "fixture catalog outage"),
	}
	service := NewLLMServiceWithBackend(root, backend)
	if _, err := service.AddCustomProvider(t.Context(), "outage-fixture", CustomLLMProviderConfig{
		Name: "Outage Fixture", Protocol: llm.ProtocolOpenAI, BaseURL: "https://outage.example/v1",
		Model: "fixture/model", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProvider(t.Context(), "outage-fixture"); err != nil {
		t.Fatal(err)
	}
	before, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.ModelCatalog(t.Context(), "outage-fixture", LLMModelQuery{Refresh: true}); !llm.IsCategory(err, llm.ErrorTransport) {
		t.Fatalf("catalog refresh error=%v", err)
	}
	if _, err := service.Probe(t.Context(), "outage-fixture"); !llm.IsCategory(err, llm.ErrorUnavailable) {
		t.Fatalf("probe error=%v", err)
	}

	after, err := NewLLMServiceWithBackend(root, backend).Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("provider outage/refresh mutated persisted state:\nbefore=%#v\nafter=%#v", before, after)
	}
	if after.ActiveProvider != "outage-fixture" {
		t.Fatalf("provider outage implicitly changed active selection to %q", after.ActiveProvider)
	}
}

func TestLLMProviderRuntimeFailureMatrixRemainsTypedAndDoesNotMutatePersistedState(t *testing.T) {
	for _, category := range []llm.ErrorCategory{
		llm.ErrorUnavailable,
		llm.ErrorUnauthorized,
		llm.ErrorRateLimited,
		llm.ErrorInvalidResponse,
	} {
		t.Run(string(category), func(t *testing.T) {
			root := isolateSettingServiceConfig(t)
			backend := &llmSecurityMatrixBackend{
				inferErr:    llm.NewError(category, "", "fixture inference failure"),
				discoverErr: llm.NewError(category, "", "fixture discovery failure"),
			}
			service := NewLLMServiceWithBackend(root, backend)
			if _, err := service.AddCustomProvider(t.Context(), "failure-fixture", CustomLLMProviderConfig{
				Name: "Failure Fixture", Protocol: llm.ProtocolOpenAI, BaseURL: "https://failure.example/v1",
				Model: "fixture/model", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SelectProvider(t.Context(), "failure-fixture"); err != nil {
				t.Fatal(err)
			}
			before, err := service.Catalog(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ModelCatalog(t.Context(), "failure-fixture", LLMModelQuery{Refresh: true}); !llm.IsCategory(err, category) {
				t.Fatalf("catalog failure category=%s err=%v", category, err)
			}
			if _, err := service.Probe(t.Context(), "failure-fixture"); !llm.IsCategory(err, category) {
				t.Fatalf("probe failure category=%s err=%v", category, err)
			}
			after, err := NewLLMServiceWithBackend(root, backend).Catalog(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("provider failure %s mutated persisted state:\nbefore=%#v\nafter=%#v", category, before, after)
			}
			if after.ActiveProvider != "failure-fixture" {
				t.Fatalf("provider failure %s changed active selection to %q", category, after.ActiveProvider)
			}
		})
	}
}

func TestLLMOllamaFreshCloudDefaultAndPersistedLocalOrCustomStateSurviveReopen(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	fresh := NewLLMService(root)
	provider, err := fresh.Provider(t.Context(), string(llm.OllamaID))
	if err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != llm.OllamaCloudBaseURL || provider.AuthMode != llm.AuthBearer || provider.Discovery != llm.DiscoveryOllamaTags {
		t.Fatalf("fresh Ollama is not cloud-first: %#v", provider)
	}

	if _, err := fresh.SetOllamaMode(t.Context(), llm.OllamaModeLocal); err != nil {
		t.Fatal(err)
	}
	reopened := NewLLMService(root)
	local, err := reopened.Provider(t.Context(), string(llm.OllamaID))
	if err != nil {
		t.Fatal(err)
	}
	if local.BaseURL != llm.OllamaLocalBaseURL || local.AuthMode != llm.AuthNone || local.Discovery != llm.DiscoveryOllamaTags {
		t.Fatalf("persisted local Ollama state was not preserved: %#v", local)
	}
	if active, err := reopened.ActiveProvider(t.Context()); err != nil || active.ID != llm.OllamaID {
		t.Fatalf("Ollama mode persistence changed active provider: %#v err=%v", active, err)
	}

	if _, err := reopened.SetOllamaMode(t.Context(), llm.OllamaModeCloud); err != nil {
		t.Fatal(err)
	}
	const customEndpoint = "https://ollama.custom.example/proxy/v1"
	settings := NewSettingService(reopened)
	if _, err := settings.Set(t.Context(), "llm.providers[ollama].base_url", customEndpoint); err != nil {
		t.Fatal(err)
	}
	reopenedAgain := NewLLMService(root)
	custom, err := reopenedAgain.Provider(t.Context(), string(llm.OllamaID))
	if err != nil {
		t.Fatal(err)
	}
	if custom.BaseURL != customEndpoint || custom.AuthMode != llm.AuthBearer || custom.Discovery != llm.DiscoveryOllamaTags {
		t.Fatalf("persisted custom Ollama endpoint was not preserved: %#v", custom)
	}
	if class, err := reopenedAgain.OllamaEndpointClass(t.Context()); err != nil || class != llm.OllamaEndpointCustom {
		t.Fatalf("persisted custom endpoint class=%q err=%v", class, err)
	}
}

func TestLLMCredentialSecurityMatrixKeepsRawSecretOutOfResultsErrorsTraceAndActivity(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	t.Cleanup(restore)
	service := NewLLMService(root)
	dispatcher := NewDispatcher()
	if err := BindLLMOperations(dispatcher, service); err != nil {
		t.Fatal(err)
	}
	observations := make([]OperationObservation, 0, 2)
	dispatcher.SetObserver(func(_ context.Context, observation OperationObservation) {
		observations = append(observations, observation)
	})
	var events []tracepkg.Event
	ctx := tracepkg.WithObserver(WithOperationInterface(t.Context(), OperationInterfaceCLI), func(event tracepkg.Event) {
		events = append(events, event)
	})
	const secret = "sk-llm-security-matrix-secret-never-expose"

	result, err := dispatcher.Dispatch(ctx, DispatchRequest{
		Operation: capability.LLMProviderCredentialSet,
		Input:     LLMProviderCredentialInput{ID: string(llm.OllamaID), APIKey: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := result.Value.(LLMCredentialResult)
	if !ok || !credential.Configured || credential.Preview == "" || strings.Contains(credential.Preview, secret) {
		t.Fatalf("credential result=%#v", result.Value)
	}
	stored, err := llm.LoadCredential(root, string(llm.OllamaID))
	if err != nil || stored != secret {
		t.Fatalf("canonical secret store value=%q err=%v", stored, err)
	}
	status, err := service.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%#v", status), secret) {
		t.Fatalf("status leaked raw credential: %#v", status)
	}
	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if encoded, marshalErr := json.Marshal(catalog); marshalErr != nil {
		t.Fatal(marshalErr)
	} else if strings.Contains(string(encoded), secret) {
		t.Fatalf("provider persistence leaked raw credential: %s", encoded)
	}

	_, failure := dispatcher.Dispatch(ctx, DispatchRequest{
		Operation: capability.LLMProviderCredentialSet,
		Input:     LLMProviderCredentialInput{ID: "missing-provider", APIKey: secret},
	})
	if failure == nil || strings.Contains(failure.Error(), secret) {
		t.Fatalf("credential failure leaked secret or unexpectedly succeeded: %v", failure)
	}
	encoded, err := json.Marshal(struct {
		Credential   LLMCredentialResult
		Status       LLMStatusResult
		Observations []OperationObservation
		Trace        []tracepkg.Event
	}{credential, status, observations, events})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("credential secret leaked into result/activity/trace metadata: %s", encoded)
	}
	if len(observations) != 2 || !observations[0].Success || observations[1].Success {
		t.Fatalf("operation activity observations=%#v", observations)
	}
}
