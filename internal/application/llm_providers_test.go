package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestCustomProviderCRUDSelectionAndCredentialLifecycle(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewLLMService(root)

	alphaKey := "sk-alpha-custom-secret"
	betaKey := "sk-beta-custom-secret"
	alpha, err := service.AddCustomProvider(t.Context(), " Alpha.Provider ", CustomLLMProviderConfig{
		Name:      "Alpha",
		Protocol:  llm.ProtocolOpenAI,
		BaseURL:   "https://alpha.example/v1",
		Model:     "alpha/model",
		AuthMode:  llm.AuthBearer,
		Discovery: llm.DiscoveryOpenAIModels,
		APIKey:    &alphaKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if alpha.ID != "alpha.provider" || alpha.CoreKind != llm.CoreNone {
		t.Fatalf("alpha=%#v", alpha)
	}
	beta, err := service.AddCustomProvider(t.Context(), "beta", CustomLLMProviderConfig{
		Name:      "Beta",
		Protocol:  llm.ProtocolAnthropic,
		BaseURL:   "https://beta.example",
		Model:     "beta-model",
		AuthMode:  llm.AuthAPIKey,
		Discovery: llm.DiscoveryNone,
		APIKey:    &betaKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if beta.Protocol != llm.ProtocolAnthropic || beta.AuthMode != llm.AuthAPIKey {
		t.Fatalf("beta=%#v", beta)
	}
	if _, err := service.AddCustomProvider(t.Context(), "incomplete", CustomLLMProviderConfig{
		Protocol:  llm.ProtocolOpenAI,
		BaseURL:   "https://offline.example/v1",
		AuthMode:  llm.AuthBearer,
		Discovery: llm.DiscoveryNone,
	}); err != nil {
		t.Fatal(err)
	}

	catalog, err := service.Catalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Providers) != 5 || catalog.ActiveProvider != llm.OpenRouterID {
		t.Fatalf("catalog=%#v", catalog)
	}

	status, err := service.SelectProvider(t.Context(), "alpha.provider")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Selected || !status.Configured || status.Readiness != llm.ReadinessUnknown || status.Reason != "" {
		t.Fatalf("alpha selection=%#v", status)
	}

	status, err = service.SelectProvider(t.Context(), "incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Selected || status.Configured || status.Readiness != llm.ReadinessUnknown || !strings.Contains(status.Reason, "model is not configured") || !strings.Contains(status.Reason, "API key is not configured") {
		t.Fatalf("misconfigured selection=%#v", status)
	}
	if err := service.RemoveProvider(t.Context(), "incomplete"); !llm.IsCategory(err, llm.ErrorActiveRemoval) {
		t.Fatalf("active removal err=%v", err)
	}

	if _, err := service.SelectProvider(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveProvider(t.Context(), "alpha.provider"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Provider(t.Context(), "alpha.provider"); !llm.IsCategory(err, llm.ErrorProviderNotFound) {
		t.Fatalf("removed provider err=%v", err)
	}
	if _, err := llm.LoadCredential(root, "alpha.provider"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("removed credential err=%v", err)
	}
	if got, err := llm.LoadCredential(root, "beta"); err != nil || got != betaKey {
		t.Fatalf("beta credential=%q err=%v", got, err)
	}

	active, err := service.ActiveProvider(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "beta" || active.Model != "beta-model" {
		t.Fatalf("active=%#v", active)
	}
	if err := service.RemoveProvider(t.Context(), string(llm.OpenRouterID)); !llm.IsCategory(err, llm.ErrorCoreInvariant) {
		t.Fatalf("core removal err=%v", err)
	}
	if _, err := service.AddCustomProvider(t.Context(), string(llm.OllamaID), CustomLLMProviderConfig{
		Protocol: llm.ProtocolOpenAI, BaseURL: "https://example.test/v1", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryNone,
	}); !llm.IsCategory(err, llm.ErrorReservedID) {
		t.Fatalf("reserved collision err=%v", err)
	}
	if _, err := service.AddCustomProvider(t.Context(), "beta", CustomLLMProviderConfig{
		Protocol: llm.ProtocolOpenAI, BaseURL: "https://duplicate.test/v1", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryNone,
	}); !llm.IsCategory(err, llm.ErrorDuplicateID) {
		t.Fatalf("duplicate err=%v", err)
	}

	storeBytes, err := os.ReadFile(llm.NewStore(root).Path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(storeBytes, []byte(betaKey)) {
		t.Fatal("raw custom provider credential was persisted in provider store")
	}
	preview, configured, err := service.CredentialPreview(t.Context(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	if !configured || preview != tracepkg.MaskSecret(betaKey, true) || strings.Contains(preview, betaKey) {
		t.Fatalf("credential preview=%q configured=%v", preview, configured)
	}
	encoded, err := json.Marshal(CustomLLMProviderConfig{APIKey: &betaKey})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(betaKey)) || bytes.Contains(encoded, []byte("APIKey")) || bytes.Contains(encoded, []byte("api_key")) {
		t.Fatalf("custom provider read model leaked API key field: %s", encoded)
	}
}

func TestConfigureCustomProviderIsAtomicAndPreservesSelectionAndCredentialOnFailure(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewLLMService(root)
	oldKey := "old-custom-key"
	if _, err := service.AddCustomProvider(t.Context(), "switchable", CustomLLMProviderConfig{
		Name:      "Switchable",
		Protocol:  llm.ProtocolOpenAI,
		BaseURL:   "https://old.example/v1",
		Model:     "old-model",
		AuthMode:  llm.AuthBearer,
		Discovery: llm.DiscoveryOpenAIModels,
		APIKey:    &oldKey,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProvider(t.Context(), "switchable"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProvider(t.Context(), string(llm.OpenRouterID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectProvider(t.Context(), "switchable"); err != nil {
		t.Fatal(err)
	}
	before, err := service.Provider(t.Context(), "switchable")
	if err != nil {
		t.Fatal(err)
	}
	if before.Model != "old-model" || before.BaseURL != "https://old.example/v1" {
		t.Fatalf("provider configuration drifted after selection roundtrip: %#v", before)
	}

	newKey := "new-custom-key"
	configured, err := service.ConfigureCustomProvider(t.Context(), "switchable", CustomLLMProviderConfig{
		Name:      "Switchable Anthropic",
		Protocol:  llm.ProtocolAnthropic,
		BaseURL:   "https://new.example",
		Model:     "new-model",
		AuthMode:  llm.AuthAPIKey,
		Discovery: llm.DiscoveryNone,
		APIKey:    &newKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if configured.Protocol != llm.ProtocolAnthropic || configured.Model != "new-model" || configured.BaseURL != "https://new.example" {
		t.Fatalf("configured=%#v", configured)
	}
	active, err := service.ActiveProvider(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != "switchable" {
		t.Fatalf("configure changed active provider to %q", active.ID)
	}
	if got, err := llm.LoadCredential(root, "switchable"); err != nil || got != newKey {
		t.Fatalf("rotated credential=%q err=%v", got, err)
	}

	rejectedKey := "must-not-be-written"
	if _, err := service.ConfigureCustomProvider(t.Context(), "switchable", CustomLLMProviderConfig{
		Name:      "Invalid",
		Protocol:  llm.Protocol("invalid"),
		BaseURL:   "https://invalid.example",
		Model:     "invalid-model",
		AuthMode:  llm.AuthNone,
		Discovery: llm.DiscoveryNone,
		APIKey:    &rejectedKey,
	}); !llm.IsCategory(err, llm.ErrorInvalidProtocol) {
		t.Fatalf("invalid configure err=%v", err)
	}
	after, err := service.Provider(t.Context(), "switchable")
	if err != nil {
		t.Fatal(err)
	}
	if after.Protocol != configured.Protocol || after.BaseURL != configured.BaseURL || after.Model != configured.Model {
		t.Fatalf("failed configure mutated provider: before=%#v after=%#v", configured, after)
	}
	if got, err := llm.LoadCredential(root, "switchable"); err != nil || got != newKey {
		t.Fatalf("failed configure mutated credential=%q err=%v", got, err)
	}
}

func TestCustomOpenAIAndAnthropicProvidersProbeThroughApplicationContract(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewLLMService(root)

	openAIKey := "custom-openai-key"
	openAIServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("OpenAI request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+openAIKey {
			t.Fatalf("OpenAI authorization=%q", got)
		}
		_, _ = fmt.Fprint(w, `{"model":"served-openai","choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer openAIServer.Close()
	if _, err := service.AddCustomProvider(t.Context(), "custom-openai", CustomLLMProviderConfig{
		Protocol:  llm.ProtocolOpenAI,
		BaseURL:   openAIServer.URL + "/v1",
		Model:     "requested-openai",
		AuthMode:  llm.AuthBearer,
		Discovery: llm.DiscoveryOpenAIModels,
		APIKey:    &openAIKey,
	}); err != nil {
		t.Fatal(err)
	}

	anthropicKey := "custom-anthropic-key"
	anthropicServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/messages" {
			t.Fatalf("Anthropic request=%s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != anthropicKey {
			t.Fatalf("Anthropic x-api-key=%q", got)
		}
		if r.Header.Get("anthropic-version") != llm.AnthropicAPIVersion {
			t.Fatal("Anthropic version header missing")
		}
		_, _ = fmt.Fprint(w, `{"model":"served-anthropic","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
	}))
	defer anthropicServer.Close()
	if _, err := service.AddCustomProvider(t.Context(), "custom-anthropic", CustomLLMProviderConfig{
		Protocol:  llm.ProtocolAnthropic,
		BaseURL:   anthropicServer.URL,
		Model:     "requested-anthropic",
		AuthMode:  llm.AuthAPIKey,
		Discovery: llm.DiscoveryNone,
		APIKey:    &anthropicKey,
	}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"custom-openai", "custom-anthropic"} {
		if err := service.ProbeProvider(t.Context(), id); err != nil {
			t.Fatalf("probe %s: %v", id, err)
		}
	}
}

func TestModelCatalogCacheIsBoundedExplicitlyRefreshableAndNeverPersisted(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMService(root)
	var calls atomic.Int32
	var failRefresh atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		if failRefresh.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"remote/model-a"},{"id":"remote/model-b"}]}`)
	}))
	defer server.Close()
	if _, err := service.AddCustomProvider(t.Context(), "cached", CustomLLMProviderConfig{
		Protocol:  llm.ProtocolOpenAI,
		BaseURL:   server.URL + "/v1",
		Model:     "remote/model-a",
		AuthMode:  llm.AuthNone,
		Discovery: llm.DiscoveryOpenAIModels,
	}); err != nil {
		t.Fatal(err)
	}
	storePath := llm.NewStore(root).Path()
	before, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}

	models, err := service.ProviderModels(t.Context(), "cached")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(models) != 2 {
		t.Fatalf("initial models=%#v calls=%d", models, calls.Load())
	}
	models[0].ID = "caller-mutated"
	models, err = service.ProviderModels(t.Context(), "cached")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || models[0].ID != "remote/model-a" {
		t.Fatalf("cache isolation models=%#v calls=%d", models, calls.Load())
	}
	if _, err := service.RefreshProviderModels(t.Context(), "cached"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("explicit refresh calls=%d", calls.Load())
	}

	failRefresh.Store(true)
	if _, err := service.RefreshProviderModels(t.Context(), "cached"); !llm.IsCategory(err, llm.ErrorUnavailable) {
		t.Fatalf("failed refresh err=%v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("failed refresh calls=%d", calls.Load())
	}
	models, err = service.ProviderModels(t.Context(), "cached")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(models) != 2 {
		t.Fatalf("failed refresh discarded prior cache models=%#v calls=%d", models, calls.Load())
	}

	after, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || bytes.Contains(after, []byte("remote/model-b")) {
		t.Fatalf("remote model catalog changed persisted provider state: %s", after)
	}

	cache := newLLMModelCatalogCache()
	for index := 0; index < maxLLMModelCatalogCacheEntries+5; index++ {
		provider := llm.Provider{
			ID:        llm.ProviderID(fmt.Sprintf("cache-%02d", index)),
			Protocol:  llm.ProtocolOpenAI,
			BaseURL:   fmt.Sprintf("https://cache-%02d.example/v1", index),
			AuthMode:  llm.AuthNone,
			Discovery: llm.DiscoveryOpenAIModels,
		}
		cache.put(provider, []llm.Model{{ID: fmt.Sprintf("model-%02d", index)}})
	}
	cache.mu.Lock()
	cacheSize := len(cache.entries)
	cache.mu.Unlock()
	if cacheSize != maxLLMModelCatalogCacheEntries {
		t.Fatalf("cache size=%d want=%d", cacheSize, maxLLMModelCatalogCacheEntries)
	}
}

func TestGenericLLMSettingsInvalidateModelCatalogCache(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMService(root)
	settings := NewSettingService()
	settings.llm = service
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(w, `{"data":[{"id":"model-a"}]}`)
	}))
	defer server.Close()
	if _, err := service.AddCustomProvider(t.Context(), "settings-cache", CustomLLMProviderConfig{
		Protocol: llm.ProtocolOpenAI, BaseURL: server.URL + "/v1", Model: "model-a", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryOpenAIModels,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProviderModels(t.Context(), "settings-cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProviderModels(t.Context(), "settings-cache"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("cached calls=%d", calls.Load())
	}
	if _, err := settings.Set(t.Context(), "llm.providers[settings-cache].model", "model-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProviderModels(t.Context(), "settings-cache"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("settings mutation did not invalidate cache calls=%d", calls.Load())
	}
}

func TestProviderSelectionDoesNotPerformNetworkProbe(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMService(root)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	if _, err := service.AddCustomProvider(t.Context(), "offline-select", CustomLLMProviderConfig{
		Protocol: llm.ProtocolOpenAI, BaseURL: server.URL + "/v1", AuthMode: llm.AuthNone, Discovery: llm.DiscoveryNone,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := service.SelectProvider(t.Context(), "offline-select")
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || status.Configured || status.Readiness != llm.ReadinessUnknown {
		t.Fatalf("selection calls=%d status=%#v", calls.Load(), status)
	}
}
