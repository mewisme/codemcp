package llm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/secretstore"
)

func TestCredentialAccountUsesCanonicalLLMDomain(t *testing.T) {
	account, err := CredentialAccount(" Acme.V2 ")
	if err != nil {
		t.Fatal(err)
	}
	want := secretstore.AccountName(secretstore.DomainLLM, "acme.v2", "api-key")
	if account != want {
		t.Fatalf("credential account=%q want=%q", account, want)
	}
	if _, err := CredentialAccount("../escape"); err == nil {
		t.Fatal("unsafe provider id produced a credential account")
	}
}

func TestProviderCredentialNeverEntersProviderStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	store := NewStore(root)
	if err := store.Save(DefaultCatalog()); err != nil {
		t.Fatal(err)
	}
	const secret = "sk-llm-store-sentinel"
	change, err := CredentialChange("ollama", secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(strings.ToLower(string(raw)), "api_key") {
		t.Fatalf("provider store leaked credential: %s", raw)
	}
}

func TestRemoveProviderDeletesOwnedCredential(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	catalog := DefaultCatalog()
	catalog.Providers = append(catalog.Providers, Provider{
		ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://acme.example/v1", Model: "model",
		AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels,
	})
	store := NewStore(root)
	if err := store.Save(catalog); err != nil {
		t.Fatal(err)
	}
	change, err := CredentialChange("acme", "sk-acme-delete")
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProvider(root, "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredential(root, "acme"); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("removed provider credential err=%v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if catalogHasProvider(loaded, "acme") {
		t.Fatalf("removed provider remains in catalog: %#v", loaded)
	}
}

func TestFailedProviderMutationPreservesCredential(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	store := NewStore(root)
	if err := store.Save(DefaultCatalog()); err != nil {
		t.Fatal(err)
	}
	const secret = "sk-preserve-on-failure"
	change, err := CredentialChange("ollama", secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateWithSecrets(func(current Catalog) (Catalog, []secretstore.Change, error) {
		if err := os.RemoveAll(filepath.Join(root, "llm")); err != nil {
			return Catalog{}, nil, err
		}
		if err := os.WriteFile(filepath.Join(root, "llm"), []byte("block provider store write"), 0600); err != nil {
			return Catalog{}, nil, err
		}
		clear, changeErr := CredentialChange("ollama", "")
		if changeErr != nil {
			return Catalog{}, nil, changeErr
		}
		return current, []secretstore.Change{clear}, nil
	})
	if err == nil {
		t.Fatal("provider-store write failure unexpectedly succeeded")
	}
	stored, err := LoadCredential(root, "ollama")
	if err != nil || stored != secret {
		t.Fatalf("credential after failed mutation=%q err=%v", stored, err)
	}
}

func TestActiveProviderRemovalFailurePreservesCredential(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CM_CONFIG_DIR", root)
	restore := secretstore.UseMemoryForTesting()
	defer restore()
	catalog := DefaultCatalog()
	catalog.Providers = append(catalog.Providers, Provider{
		ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://acme.example/v1",
		AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels,
	})
	catalog.ActiveProvider = "acme"
	if err := NewStore(root).Save(catalog); err != nil {
		t.Fatal(err)
	}
	change, _ := CredentialChange("acme", "sk-active-provider")
	if err := secretstore.New(root).Apply([]secretstore.Change{change}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProvider(root, "acme"); err == nil {
		t.Fatal("active provider removal succeeded")
	}
	stored, err := LoadCredential(root, "acme")
	if err != nil || stored != "sk-active-provider" {
		t.Fatalf("active provider credential=%q err=%v", stored, err)
	}
}
