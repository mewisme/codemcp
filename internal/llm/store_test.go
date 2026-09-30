package llm

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoreMissingStateReturnsDefaultsWithoutWriting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("CM_CONFIG_DIR", root)
	store := NewStore(root)
	value, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if value.ActiveProvider != OpenRouterID || len(value.Providers) != 2 {
		t.Fatalf("defaults = %#v", value)
	}
	ollama := providerByID(t, value, OllamaID)
	if ollama.BaseURL != OllamaCloudBaseURL || ollama.AuthMode != AuthBearer || ollama.Discovery != DiscoveryOllamaTags {
		t.Fatalf("missing-store Ollama default=%#v", ollama)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("missing-store read wrote config root: %v", err)
	}
}

func TestStoreRoundTripPreservesCustomProvidersAndCoreOverrides(t *testing.T) {
	store, _ := newStoreTestRoot(t)
	value := DefaultCatalog()
	value.Providers[0].BaseURL = "https://router.example/v1"
	value.Providers[0].Model = "vendor/model"
	value.Providers[0].Capabilities = &ProviderCapabilities{StructuredOutput: true}
	value.Providers = append(value.Providers, Provider{
		ID: "acme", Name: "Acme", Protocol: ProtocolAnthropic, BaseURL: "https://llm.acme.test", Model: "claude-like",
		AuthMode: AuthAPIKey, Discovery: DiscoveryNone,
	})
	value.ActiveProvider = "acme"
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveProvider != "acme" || len(loaded.Providers) != 3 || loaded.Providers[0].BaseURL != "https://router.example/v1" || loaded.Providers[0].Model != "vendor/model" || loaded.Providers[0].Capabilities == nil || !loaded.Providers[0].Capabilities.StructuredOutput {
		t.Fatalf("round trip = %#v", loaded)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || bytes.Contains(bytes.ToLower(raw), []byte("api_key")) {
		t.Fatalf("provider store is not secret-free JSON: %s", raw)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("provider store mode = %#o", info.Mode().Perm())
	}
}

func TestStoreReconcilesMissingCoreProvidersWithoutRewriting(t *testing.T) {
	store, _ := newStoreTestRoot(t)
	custom := Provider{ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://example.com/v1", Model: "model", AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels}
	raw := []byte(`{"version":1,"active_provider":"acme","providers":[{"id":"acme","name":"Acme","protocol":"openai","base_url":"https://example.com/v1","model":"model","auth_mode":"bearer","discovery":"openai-models"}]}`)
	writeRawStore(t, store, raw)
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveProvider != custom.ID || len(loaded.Providers) != 3 || !catalogHasProvider(loaded, OpenRouterID) || !catalogHasProvider(loaded, OllamaID) {
		t.Fatalf("reconciled = %#v", loaded)
	}
	ollama := providerByID(t, loaded, OllamaID)
	if ollama.BaseURL != OllamaCloudBaseURL || ollama.AuthMode != AuthBearer || ollama.Discovery != DiscoveryOllamaTags {
		t.Fatalf("reconciled Ollama default=%#v", ollama)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("load-time core reconciliation rewrote user state")
	}
}

func TestStorePreservesPersistedOllamaEndpointChoice(t *testing.T) {
	for _, test := range []struct {
		name    string
		baseURL string
		auth    AuthMode
		model   string
	}{
		{name: "local", baseURL: OllamaLocalBaseURL, auth: AuthNone, model: "qwen3:8b"},
		{name: "custom", baseURL: "https://ollama.internal.example/proxy/v1", auth: AuthBearer, model: "gemma3:27b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newStoreTestRoot(t)
			catalog := DefaultCatalog()
			for index := range catalog.Providers {
				if catalog.Providers[index].ID != OllamaID {
					continue
				}
				catalog.Providers[index].BaseURL = test.baseURL
				catalog.Providers[index].AuthMode = test.auth
				catalog.Providers[index].Model = test.model
			}
			if err := store.Save(catalog); err != nil {
				t.Fatal(err)
			}

			loaded, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			ollama := providerByID(t, loaded, OllamaID)
			if ollama.BaseURL != test.baseURL || ollama.AuthMode != test.auth || ollama.Model != test.model || ollama.Discovery != DiscoveryOllamaTags {
				t.Fatalf("persisted Ollama changed after reload: %#v", ollama)
			}
			if loaded.ActiveProvider != OpenRouterID {
				t.Fatalf("persisted Ollama endpoint changed active provider=%q", loaded.ActiveProvider)
			}
		})
	}
}

func TestStoreRejectsCorruptionWrongVersionAndNonRegularPathsWithoutRewrite(t *testing.T) {
	for name, raw := range map[string][]byte{
		"corrupt": []byte(`{"version":1,"active_provider":`),
		"version": []byte(`{"version":2,"active_provider":"openrouter","providers":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			store, _ := newStoreTestRoot(t)
			writeRawStore(t, store, raw)
			before, _ := os.ReadFile(store.Path())
			if _, err := store.Load(); err == nil {
				t.Fatal("invalid store was accepted")
			}
			after, _ := os.ReadFile(store.Path())
			if !bytes.Equal(before, after) {
				t.Fatal("failed load rewrote invalid state")
			}
		})
	}

	t.Run("directory", func(t *testing.T) {
		store, _ := newStoreTestRoot(t)
		if err := os.MkdirAll(store.Path(), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("directory error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		store, root := newStoreTestRoot(t)
		target := filepath.Join(root, "target.json")
		if err := os.MkdirAll(filepath.Dir(store.Path()), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(`{"version":1}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, store.Path()); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("symlink error = %v", err)
		}
	})
}

func TestStoreUpdateSerializesConcurrentReadModifyWrite(t *testing.T) {
	_, root := newStoreTestRoot(t)
	first := NewStore(root)
	second := NewStore(root)
	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		store *Store
		id    ProviderID
	}{
		{first, "alpha"},
		{second, "beta"},
	} {
		wg.Add(1)
		go func(store *Store, id ProviderID) {
			defer wg.Done()
			<-start
			_, err := store.Update(func(value Catalog) (Catalog, error) {
				value.Providers = append(value.Providers, Provider{ID: id, Name: strings.ToUpper(string(id)), Protocol: ProtocolOpenAI, BaseURL: "https://" + string(id) + ".example/v1", AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels})
				return value, nil
			})
			errorsByWriter <- err
		}(item.store, item.id)
	}
	close(start)
	wg.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := first.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 4 || !catalogHasProvider(loaded, "alpha") || !catalogHasProvider(loaded, "beta") {
		t.Fatalf("concurrent update lost provider: %#v", loaded)
	}
}

func TestPortableStoreJSONRejectsUnknownCredentialFields(t *testing.T) {
	secret := "sk-do-not-leak"
	raw := []byte(`{"version":1,"active_provider":"openrouter","providers":[{"id":"openrouter","name":"OpenRouter","protocol":"openai","base_url":"https://openrouter.ai/api/v1","model":"openrouter/free","auth_mode":"bearer","discovery":"openai-models","core_kind":"openrouter","api_key":"` + secret + `"}]}`)
	_, err := NormalizePortableStoreJSON(raw)
	if err == nil {
		t.Fatal("credential-bearing store was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credential-bearing store error leaked secret: %v", err)
	}
}

func newStoreTestRoot(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	return NewStore(root), root
}

func writeRawStore(t *testing.T, store *Store, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func catalogHasProvider(value Catalog, id ProviderID) bool {
	for _, provider := range value.Providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func providerByID(t *testing.T, value Catalog, id ProviderID) Provider {
	t.Helper()
	for _, provider := range value.Providers {
		if provider.ID == id {
			return provider
		}
	}
	t.Fatalf("provider %q not found in %#v", id, value)
	return Provider{}
}
