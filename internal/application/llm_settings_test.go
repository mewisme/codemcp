package application

import (
	"errors"
	"os"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/llm"
	"go.mewis.me/codemcp/internal/secretstore"
	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func TestLLMSettingAliasesAndProviderSelectorsConverge(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewSettingService()

	initial, err := service.Present(t.Context(), "llm.api_key")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Value != "not configured" || initial.Configured == nil || *initial.Configured {
		t.Fatalf("initial credential presentation=%#v", initial)
	}
	if _, err := service.Read(t.Context(), "llm.api_key"); err == nil {
		t.Fatal("raw active LLM credential became readable")
	}

	const secret = "sk-llm-setting-secret-sentinel"
	configured, err := service.Set(t.Context(), "llm.api_key", secret)
	if err != nil {
		t.Fatal(err)
	}
	wantPreview := tracepkg.MaskSecret(secret, true)
	if configured.Value != wantPreview || configured.Configured == nil || !*configured.Configured || strings.Contains(configured.Value, secret) {
		t.Fatalf("configured credential presentation=%#v", configured)
	}
	scoped, err := service.Present(t.Context(), "llm.providers[openrouter].api_key")
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Value != wantPreview || scoped.Configured == nil || !*scoped.Configured {
		t.Fatalf("scoped credential presentation=%#v", scoped)
	}

	if _, err := service.Set(t.Context(), "llm.base_url", "https://router.example/v1"); err != nil {
		t.Fatal(err)
	}
	scopedURL, err := service.Read(t.Context(), "llm.providers[openrouter].base_url")
	if err != nil || scopedURL.Value != "https://router.example/v1" {
		t.Fatalf("scoped base URL=%#v err=%v", scopedURL, err)
	}
	if _, err := service.Set(t.Context(), "llm.providers[openrouter].model", "vendor/model"); err != nil {
		t.Fatal(err)
	}
	activeModel, err := service.Read(t.Context(), "llm.model")
	if err != nil || activeModel.Value != "vendor/model" {
		t.Fatalf("active model=%#v err=%v", activeModel, err)
	}

	if _, err := service.Apply(t.Context(), []SettingChange{
		{Key: "llm.provider", Value: "ollama"},
		{Key: "llm.model", Value: "qwen3"},
	}); err != nil {
		t.Fatal(err)
	}
	active, err := service.Read(t.Context(), "llm.provider")
	if err != nil || active.Value != "ollama" {
		t.Fatalf("active provider=%#v err=%v", active, err)
	}
	ollamaModel, err := service.Read(t.Context(), "llm.providers[ollama].model")
	if err != nil || ollamaModel.Value != "qwen3" {
		t.Fatalf("Ollama model=%#v err=%v", ollamaModel, err)
	}

	raw, err := os.ReadFile(llm.NewStore(root).Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(strings.ToLower(string(raw)), "api_key") {
		t.Fatalf("provider JSON leaked credential: %s", raw)
	}
}

func TestLLMSettingSelectorsResolveOnlyRegisteredProviders(t *testing.T) {
	isolateSettingServiceConfig(t)
	service := NewSettingService()
	for _, key := range []string{
		"llm.providers[missing].model",
		"llm.providers[../openrouter].model",
		"llm.providers[openrouter%2Fescape].model",
	} {
		if _, err := service.Read(t.Context(), key); err == nil {
			t.Fatalf("unsafe or unregistered selector resolved: %s", key)
		}
	}
	if result, err := service.Read(t.Context(), "llm.providers[openrouter].core"); err != nil || result.Value != "true" {
		t.Fatalf("registered provider core setting=%#v err=%v", result, err)
	}
	if _, err := service.Set(t.Context(), "llm.providers[openrouter].core", "false"); err == nil {
		t.Fatal("derived core identity became writable")
	}
}

func TestLLMSecretPurgeAndUninitializeLeaveNoManagedCredential(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	service := NewSettingService()
	const secret = "sk-llm-purge-sentinel"
	if _, err := service.Set(t.Context(), "llm.api_key", secret); err != nil {
		t.Fatal(err)
	}
	if err := PurgeStoredSecretsContext(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := llm.LoadCredential(root, "openrouter"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("credential survived purge: %v", err)
	}
	if _, err := service.Set(t.Context(), "llm.api_key", secret); err != nil {
		t.Fatal(err)
	}
	if err := UninitializeContext(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := llm.LoadCredential(root, "openrouter"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("credential survived uninitialize: %v", err)
	}
	if _, err := os.Stat(llm.NewStore(root).Path()); !os.IsNotExist(err) {
		t.Fatalf("LLM provider store survived uninitialize: %v", err)
	}
}
