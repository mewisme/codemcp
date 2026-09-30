package application

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestOllamaModeSwitchUpdatesDefaultsCoherentlyAndPreservesSelection(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMService(root)
	store := llm.NewStore(root)
	catalog, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for index := range catalog.Providers {
		if catalog.Providers[index].ID == llm.OllamaID {
			catalog.Providers[index].Model = "qwen3:8b"
		}
	}
	if err := store.Save(catalog); err != nil {
		t.Fatal(err)
	}

	cloud, err := service.SetOllamaMode(t.Context(), llm.OllamaModeCloud)
	if err != nil {
		t.Fatal(err)
	}
	if cloud.BaseURL != llm.OllamaCloudBaseURL || cloud.AuthMode != llm.AuthBearer || cloud.Model != "qwen3:8b" {
		t.Fatalf("cloud=%#v", cloud)
	}
	if class, err := service.OllamaEndpointClass(t.Context()); err != nil || class != llm.OllamaEndpointCloud {
		t.Fatalf("cloud class=%q err=%v", class, err)
	}
	catalog, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ActiveProvider != llm.OllamaID {
		t.Fatalf("mode switch changed active provider to %q", catalog.ActiveProvider)
	}

	local, err := service.SetOllamaMode(t.Context(), llm.OllamaModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.BaseURL != llm.OllamaLocalBaseURL || local.AuthMode != llm.AuthNone || local.Model != "qwen3:8b" {
		t.Fatalf("local=%#v", local)
	}
}

func TestOllamaCustomEndpointOverrideRetainsNativeDiscoveryAndCanReturnToModeDefaults(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	service := NewLLMService(root)
	if _, err := service.SetOllamaMode(t.Context(), llm.OllamaModeCloud); err != nil {
		t.Fatal(err)
	}
	settings := NewSettingService()
	const custom = "https://ollama.internal.example/proxy/v1"
	if _, err := settings.Set(t.Context(), "llm.providers[ollama].base_url", custom); err != nil {
		t.Fatal(err)
	}
	provider, err := service.Provider(t.Context(), string(llm.OllamaID))
	if err != nil {
		t.Fatal(err)
	}
	if provider.BaseURL != custom || provider.AuthMode != llm.AuthBearer || provider.Discovery != llm.DiscoveryOllamaTags {
		t.Fatalf("custom provider=%#v", provider)
	}
	if class, err := service.OllamaEndpointClass(t.Context()); err != nil || class != llm.OllamaEndpointCustom {
		t.Fatalf("custom class=%q err=%v", class, err)
	}

	local, err := service.SetOllamaMode(t.Context(), llm.OllamaModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.BaseURL != llm.OllamaLocalBaseURL || local.AuthMode != llm.AuthNone || local.Discovery != llm.DiscoveryOllamaTags {
		t.Fatalf("reconciled local=%#v", local)
	}
}

func TestOllamaLocalModelsAndProbeUseNativeTagsAndNoKeyOpenAIRoute(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	var tagsCalls atomic.Int32
	var inferenceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("local authorization=%q", got)
		}
		switch r.URL.Path {
		case "/api/tags":
			tagsCalls.Add(1)
			_, _ = fmt.Fprint(w, `{"models":[{"name":"qwen3:8b","model":"qwen3:8b"}]}`)
		case "/v1/chat/completions":
			inferenceCalls.Add(1)
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["model"] != "qwen3:8b" {
				t.Fatalf("model=%v", request["model"])
			}
			_, _ = fmt.Fprint(w, `{"model":"qwen3:8b","choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	configureOllamaForApplicationTest(t, root, server.URL+"/v1", llm.AuthNone, "qwen3:8b")

	service := NewLLMService(root)
	models, err := service.OllamaModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "qwen3:8b" || models[0].Name != "qwen3:8b" {
		t.Fatalf("models=%#v", models)
	}
	if err := service.ProbeOllama(t.Context()); err != nil {
		t.Fatal(err)
	}
	if tagsCalls.Load() != 1 || inferenceCalls.Load() != 1 {
		t.Fatalf("calls tags=%d inference=%d", tagsCalls.Load(), inferenceCalls.Load())
	}
}

func TestOllamaCloudWireUsesBearerForTagsAndOpenAIRoute(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	const credential = "ollama-cloud-api-key"
	const model = "gemma4:31b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		switch r.URL.Path {
		case "/api/tags":
			_, _ = fmt.Fprintf(w, `{"models":[{"name":%q,"model":%q}]}`, model, model)
		case "/v1/chat/completions":
			_, _ = fmt.Fprintf(w, `{"model":%q,"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`, model)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	configureOllamaForApplicationTest(t, root, server.URL+"/v1", llm.AuthBearer, model)
	change, err := llm.CredentialChange(string(llm.OllamaID), credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}

	service := NewLLMService(root)
	models, err := service.OllamaModels(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Name != model {
		t.Fatalf("models=%#v", models)
	}
	if err := service.ProbeOllama(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaProbeMapsMissingDaemonToUnavailableWithoutStartupDependency(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	baseURL := server.URL + "/v1"
	server.Close()
	configureOllamaForApplicationTest(t, root, baseURL, llm.AuthNone, "qwen3:8b")

	service := NewLLMService(root)
	if _, err := service.Catalog(t.Context()); err != nil {
		t.Fatalf("catalog load depends on daemon availability: %v", err)
	}
	if err := service.ProbeOllama(t.Context()); !llm.IsCategory(err, llm.ErrorUnavailable) {
		t.Fatalf("missing daemon probe err=%v", err)
	}
}

func TestOllamaCoreWireSettingsCannotBreakProtocolDiscoveryOrKnownModeAuth(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	for _, test := range []struct {
		key   string
		value string
	}{
		{key: "llm.providers[ollama].protocol", value: string(llm.ProtocolAnthropic)},
		{key: "llm.providers[ollama].discovery", value: string(llm.DiscoveryOpenAIModels)},
		{key: "llm.providers[ollama].auth_mode", value: string(llm.AuthNone)},
	} {
		if _, err := service.Set(t.Context(), test.key, test.value); !llm.IsCategory(err, llm.ErrorCoreInvariant) {
			t.Fatalf("%s=%q err=%v", test.key, test.value, err)
		}
	}
}

func configureOllamaForApplicationTest(t *testing.T, root, baseURL string, auth llm.AuthMode, model string) {
	t.Helper()
	store := llm.NewStore(root)
	catalog, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for index := range catalog.Providers {
		if catalog.Providers[index].ID != llm.OllamaID {
			continue
		}
		catalog.Providers[index].BaseURL = strings.TrimRight(baseURL, "/")
		catalog.Providers[index].AuthMode = auth
		catalog.Providers[index].Model = model
	}
	if err := store.Save(catalog); err != nil {
		t.Fatal(err)
	}
}
