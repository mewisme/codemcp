package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestOpenRouterModelsNormalizeFilterAndDoNotPersistRemoteCatalog(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()

	const credential = "sk-or-v1-catalog-inference-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("OpenRouter catalog authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[
			{"id":"openrouter/free","name":"OpenRouter: Free Models Router","canonical_slug":"openrouter/free-2026","context_length":200000,"created":1788220800,"pricing":{"prompt":"0","completion":"0.000000"},"architecture":{"input_modalities":["text"],"output_modalities":["text"]},"supported_parameters":["response_format","structured_outputs","tools"],"top_provider":{"max_completion_tokens":8192}},
			{"id":"vendor/model-paid","name":"Vendor Paid","canonical_slug":"vendor/model-paid-2026","context_length":131072,"created":1788307200,"pricing":{"prompt":"0.000001","completion":"0.000002"},"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"supported_parameters":["temperature","tools","reasoning"],"top_provider":{"max_completion_tokens":16384}},
			{"id":"vendor/model-free","name":"Vendor Free","context_length":65536,"pricing":{"prompt":"0.000000","completion":"0"},"supported_parameters":["response_format"]}
		]}`)
	}))
	defer server.Close()

	configureOpenRouterForApplicationTest(t, root, server.URL+"/api/v1", llm.OpenRouterDefaultModel, llm.OpenRouterID)
	credentialChange, err := llm.CredentialChange(string(llm.OpenRouterID), credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{credentialChange}); err != nil {
		t.Fatal(err)
	}
	store := llm.NewStore(root)
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}

	free := true
	page, err := NewLLMService(root).OpenRouterModels(t.Context(), LLMModelQuery{Free: &free, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.ProviderID != llm.OpenRouterID || page.Matched != 2 || len(page.Models) != 1 || !page.HasMore {
		t.Fatalf("page=%#v", page)
	}
	model := page.Models[0]
	if model.ID != llm.OpenRouterDefaultModel || model.Name != "OpenRouter: Free Models Router" || model.ContextLength != 200000 || model.PromptPrice != "0" || model.CompletionPrice != "0.000000" || !model.Free || !model.SupportsStructuredOutput {
		t.Fatalf("normalized model=%#v", model)
	}

	searched, err := NewLLMService(root).OpenRouterModels(t.Context(), LLMModelQuery{Search: "VENDOR FREE", Free: &free})
	if err != nil {
		t.Fatal(err)
	}
	if searched.Matched != 1 || len(searched.Models) != 1 || searched.Models[0].ID != "vendor/model-free" {
		t.Fatalf("searched=%#v", searched)
	}
	if searched.Models[0].SupportsStructuredOutput {
		t.Fatal("response_format-only model was overclaimed as strict structured-output capable")
	}
	paid := false
	minimumContext := 100000
	rich, err := NewLLMService(root).OpenRouterModels(t.Context(), LLMModelQuery{
		Authors: []string{"vendor"}, Free: &paid, MinContext: &minimumContext, MaxPromptPrice: "0.000001",
		Capabilities: []string{"tools"}, InputModalities: []string{"image"}, All: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rich.Matched != 1 || len(rich.Models) != 1 {
		t.Fatalf("rich OpenRouter query=%#v", rich)
	}
	paidModel := rich.Models[0]
	if paidModel.ID != "vendor/model-paid" || paidModel.Author != "vendor" || paidModel.CanonicalSlug != "vendor/model-paid-2026" || !paidModel.ContextLengthKnown || !paidModel.PricingKnown || !paidModel.FreeKnown || paidModel.Free || paidModel.CreatedAt == nil || !paidModel.ModalitiesKnown || paidModel.MaxOutputTokens != 16384 || !paidModel.MaxOutputTokensKnown || !containsString(paidModel.Capabilities, "reasoning") {
		t.Fatalf("rich OpenRouter model=%#v", paidModel)
	}

	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("remote OpenRouter catalog mutated persisted provider configuration")
	}
	if bytes.Contains(after, []byte("vendor/model-paid")) || bytes.Contains(after, []byte("vendor/model-free")) {
		t.Fatalf("remote catalog was persisted: %s", after)
	}
}

func TestOpenRouterCoreProfileCannotBeOverriddenThroughGenericSettings(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "llm.providers[openrouter].protocol", value: string(llm.ProtocolAnthropic)},
		{key: "llm.providers[openrouter].auth_mode", value: string(llm.AuthNone)},
		{key: "llm.providers[openrouter].discovery", value: string(llm.DiscoveryNone)},
	} {
		if _, err := service.Set(t.Context(), test.key, test.value); !llm.IsCategory(err, llm.ErrorCoreInvariant) {
			t.Fatalf("%s=%q err=%v", test.key, test.value, err)
		}
	}
}

func TestOpenRouterModelCatalogFailureLeavesProviderStateUnchanged(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-or-v1-malformed-catalog" {
			t.Fatalf("authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{malformed`)
	}))
	defer server.Close()
	configureOpenRouterForApplicationTest(t, root, server.URL+"/api/v1", "user/selected-model", llm.OllamaID)
	change, err := llm.CredentialChange(string(llm.OpenRouterID), "sk-or-v1-malformed-catalog")
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}

	store := llm.NewStore(root)
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLLMService(root).OpenRouterModels(t.Context(), LLMModelQuery{}); !llm.IsCategory(err, llm.ErrorInvalidResponse) {
		t.Fatalf("catalog error=%v", err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed OpenRouter catalog refresh mutated persisted provider configuration")
	}
}

func TestOpenRouterModelSelectionIsOfflineAndProbeUsesConfiguredModelAndCredential(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	const credential = "sk-or-v1-probe-secret"
	const selectedModel = "vendor/user-entered-model"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected OpenRouter path: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if got, _ := request["model"].(string); got != selectedModel {
			t.Fatalf("probe model=%q", got)
		}
		_, _ = fmt.Fprint(w, `{"model":"vendor/served-model","choices":[{"message":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1}}`)
	}))
	defer server.Close()

	configureOpenRouterForApplicationTest(t, root, server.URL+"/api/v1", llm.OpenRouterDefaultModel, llm.OllamaID)
	change, err := llm.CredentialChange(string(llm.OpenRouterID), credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}

	settings := NewSettingService()
	if _, err := settings.Set(t.Context(), "llm.providers[openrouter].model", selectedModel); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("offline model selection made %d network call(s)", calls.Load())
	}

	if err := NewLLMService(root).ProbeOpenRouter(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("probe calls=%d", calls.Load())
	}
	catalog, err := llm.NewStore(root).Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ActiveProvider != llm.OllamaID {
		t.Fatalf("probe changed active provider to %q", catalog.ActiveProvider)
	}
	provider := findLLMProviderForApplicationTest(t, catalog, llm.OpenRouterID)
	if provider.Model != selectedModel {
		t.Fatalf("probe changed configured model to %q", provider.Model)
	}
}

func TestOpenRouterProbeMapsRateLimitAndMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		category llm.ErrorCategory
	}{
		{name: "rate_limit", status: http.StatusTooManyRequests, body: `{"error":{"message":"rate limited"}}`, category: llm.ErrorRateLimited},
		{name: "malformed", status: http.StatusOK, body: `{not-json`, category: llm.ErrorInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := isolateSettingServiceConfig(t)
			restore := secretstore.UseMemoryForTesting()
			defer restore()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/keys") || strings.Contains(r.URL.Path, "/guardrails") {
					t.Fatalf("inference credential reached management endpoint %q", r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			configureOpenRouterForApplicationTest(t, root, server.URL+"/api/v1", llm.OpenRouterDefaultModel, llm.OpenRouterID)
			change, err := llm.CredentialChange(string(llm.OpenRouterID), "sk-or-v1-inference-only")
			if err != nil {
				t.Fatal(err)
			}
			if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
				t.Fatal(err)
			}
			if err := NewLLMService(root).ProbeOpenRouter(t.Context()); !llm.IsCategory(err, test.category) {
				t.Fatalf("probe err=%v category=%s", err, test.category)
			}
		})
	}
}

func TestOpenRouterModelQueryBounds(t *testing.T) {
	if _, err := normalizeLLMModelQuery(LLMModelQuery{Search: strings.Repeat("x", maxLLMModelSearchBytes+1)}); err == nil {
		t.Fatal("oversized model search was accepted")
	}
	if _, err := normalizeLLMModelQuery(LLMModelQuery{Limit: maxLLMModelReadLimit + 1}); err == nil {
		t.Fatal("oversized model limit was accepted")
	}
	query, err := normalizeLLMModelQuery(LLMModelQuery{})
	if err != nil || query.Limit != defaultLLMModelReadLimit {
		t.Fatalf("default query=%#v err=%v", query, err)
	}
}

func configureOpenRouterForApplicationTest(t *testing.T, root, baseURL, model string, active llm.ProviderID) {
	t.Helper()
	store := llm.NewStore(root)
	catalog, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for index := range catalog.Providers {
		if catalog.Providers[index].ID != llm.OpenRouterID {
			continue
		}
		catalog.Providers[index].BaseURL = baseURL
		catalog.Providers[index].Model = model
	}
	catalog.ActiveProvider = active
	if err := store.Save(catalog); err != nil {
		t.Fatal(err)
	}
}

func findLLMProviderForApplicationTest(t *testing.T, catalog llm.Catalog, id llm.ProviderID) llm.Provider {
	t.Helper()
	for _, provider := range catalog.Providers {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q missing from catalog", id)
	return llm.Provider{}
}
