package llm

import (
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogLocksCoreIdentityAndSelection(t *testing.T) {
	value, err := NormalizeCatalog(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if value.ActiveProvider != OllamaID {
		t.Fatalf("active provider = %q", value.ActiveProvider)
	}
	if len(value.Providers) != 1 {
		t.Fatalf("providers = %#v", value.Providers)
	}
	ollama := value.Providers[0]
	if ollama.ID != OllamaID || ollama.CoreKind != CoreOllama || ollama.BaseURL != OllamaCloudBaseURL || ollama.Protocol != ProtocolOpenAI || ollama.AuthMode != AuthBearer || ollama.Discovery != DiscoveryOllamaTags {
		t.Fatalf("ollama = %#v", ollama)
	}
}

func TestOllamaCoreDefaultsToCloudWithoutChangingActiveProvider(t *testing.T) {
	provider := DefaultOllama()
	if provider.BaseURL != OllamaCloudBaseURL || provider.AuthMode != AuthBearer || provider.Discovery != DiscoveryOllamaTags || provider.CoreKind != CoreOllama {
		t.Fatalf("Ollama defaults=%#v", provider)
	}
	catalog := DefaultCatalog()
	if catalog.ActiveProvider != OllamaID {
		t.Fatalf("default active provider=%q", catalog.ActiveProvider)
	}
}

func TestOllamaCoreIdentityIsCanonical(t *testing.T) {
	provider := DefaultOllama()
	if provider.BaseURL != OllamaCloudBaseURL || provider.Protocol != ProtocolOpenAI || provider.AuthMode != AuthBearer || provider.Discovery != DiscoveryOllamaTags || provider.CoreKind != CoreOllama {
		t.Fatalf("Ollama defaults=%#v", provider)
	}
	for name, mutate := range map[string]func(*Provider){
		"protocol":  func(value *Provider) { value.Protocol = ProtocolAnthropic },
		"auth":      func(value *Provider) { value.AuthMode = AuthAPIKey },
		"discovery": func(value *Provider) { value.Discovery = DiscoveryNone },
	} {
		t.Run(name, func(t *testing.T) {
			value := DefaultOllama()
			mutate(&value)
			catalog := DefaultCatalog()
			catalog.Providers[0] = value
			if _, err := NormalizeCatalog(catalog); !IsCategory(err, ErrorCoreInvariant) {
				t.Fatalf("Ollama core mutation err=%v", err)
			}
		})
	}

	value := DefaultOllama()
	value.BaseURL = "https://router.example/v1"
	value.Model = "vendor/user-model"
	catalog := DefaultCatalog()
	catalog.Providers[0] = value
	normalized, err := NormalizeCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Providers[0].BaseURL != value.BaseURL || normalized.Providers[0].Model != value.Model {
		t.Fatalf("Ollama endpoint/model override lost: %#v", normalized.Providers[0])
	}
}

func TestProviderIDsNormalizeAndRejectUnsafeValues(t *testing.T) {
	id, err := NormalizeProviderID("  Acme.Provider_1  ")
	if err != nil || id != "acme.provider_1" {
		t.Fatalf("normalize = %q, %v", id, err)
	}
	for _, raw := range []string{"", "-bad", "bad-", "bad/id", "bad id", strings.Repeat("a", MaxProviderIDBytes+1)} {
		if _, err := NormalizeProviderID(raw); !IsCategory(err, ErrorInvalidID) {
			t.Fatalf("NormalizeProviderID(%q) error = %v", raw, err)
		}
	}
}

func TestCatalogRejectsDuplicateNormalizedIDs(t *testing.T) {
	custom := Provider{ID: "Acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://example.com/v1", AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels}
	duplicate := custom
	duplicate.ID = " acme "
	value := DefaultCatalog()
	value.Providers = append(value.Providers, custom, duplicate)
	if _, err := NormalizeCatalog(value); !IsCategory(err, ErrorDuplicateID) {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestCustomProviderCannotClaimReservedOrCoreIdentity(t *testing.T) {
	for _, value := range []Provider{
		DefaultOllama(),
		{ID: "ollama", Name: "custom", Protocol: ProtocolOpenAI, BaseURL: "https://example.com/v1", AuthMode: AuthNone, Discovery: DiscoveryNone},
	} {
		if _, err := NormalizeCustomProvider(value); !IsCategory(err, ErrorReservedID) {
			t.Fatalf("reserved provider %#v error = %v", value, err)
		}
	}
	custom := Provider{ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://example.com/v1", AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels, CoreKind: CoreOllama}
	if _, err := NormalizeCustomProvider(custom); !IsCategory(err, ErrorCoreInvariant) {
		t.Fatalf("custom core claim error = %v", err)
	}
}

func TestCoreIdentityCannotBeRenamedReclassifiedOrRemoved(t *testing.T) {
	for name, test := range map[string]struct {
		mutate   func(*Catalog)
		category ErrorCategory
	}{
		"rename":  {mutate: func(value *Catalog) { value.Providers[0].Name = "Router" }, category: ErrorCoreInvariant},
		"missing": {mutate: func(value *Catalog) { value.Providers = nil }, category: ErrorMissingCore},
	} {
		t.Run(name, func(t *testing.T) {
			value := DefaultCatalog()
			test.mutate(&value)
			_, err := NormalizeCatalog(value)
			if !IsCategory(err, test.category) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if err := ValidateProviderRemoval(DefaultCatalog(), string(OllamaID)); !IsCategory(err, ErrorCoreInvariant) {
		t.Fatalf("core removal error = %v", err)
	}
}

func TestCatalogRequiresExactlyOneActiveProviderReference(t *testing.T) {
	value := DefaultCatalog()
	value.ActiveProvider = ""
	if _, err := NormalizeCatalog(value); !IsCategory(err, ErrorInvalidActive) {
		t.Fatalf("empty active error = %v", err)
	}
	value = DefaultCatalog()
	value.ActiveProvider = "missing"
	if _, err := NormalizeCatalog(value); !IsCategory(err, ErrorInvalidActive) {
		t.Fatalf("missing active error = %v", err)
	}

	typeOfProvider := reflect.TypeOf(Provider{})
	for _, forbidden := range []string{"Active", "Selected", "Enabled"} {
		if _, ok := typeOfProvider.FieldByName(forbidden); ok {
			t.Fatalf("provider record must not contain per-provider selection field %q", forbidden)
		}
	}
}

func TestActiveCustomProviderMustBeDeselectedBeforeRemoval(t *testing.T) {
	custom := Provider{ID: "acme", Name: "Acme", Protocol: ProtocolAnthropic, BaseURL: "https://example.com", AuthMode: AuthAPIKey, Discovery: DiscoveryNone}
	value := DefaultCatalog()
	value.Providers = append(value.Providers, custom)
	value.ActiveProvider = "acme"
	if err := ValidateProviderRemoval(value, "acme"); !IsCategory(err, ErrorActiveRemoval) {
		t.Fatalf("active removal error = %v", err)
	}
	value.ActiveProvider = OllamaID
	if err := ValidateProviderRemoval(value, "acme"); err != nil {
		t.Fatalf("inactive custom removal = %v", err)
	}
}

func TestProviderValidationRejectsMalformedProtocolAuthDiscoveryAndEndpoint(t *testing.T) {
	base := Provider{ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://example.com/v1", AuthMode: AuthBearer, Discovery: DiscoveryOpenAIModels}
	tests := []struct {
		name     string
		mutate   func(*Provider)
		category ErrorCategory
	}{
		{name: "protocol", mutate: func(value *Provider) { value.Protocol = "other" }, category: ErrorInvalidProtocol},
		{name: "auth", mutate: func(value *Provider) { value.AuthMode = "token" }, category: ErrorInvalidAuth},
		{name: "discovery", mutate: func(value *Provider) { value.Discovery = "magic" }, category: ErrorInvalidDiscovery},
		{name: "scheme", mutate: func(value *Provider) { value.BaseURL = "ftp://example.com" }, category: ErrorInvalidEndpoint},
		{name: "userinfo", mutate: func(value *Provider) { value.BaseURL = "https://user:pass@example.com/v1" }, category: ErrorInvalidEndpoint},
		{name: "fragment", mutate: func(value *Provider) { value.BaseURL = "https://example.com/v1#secret" }, category: ErrorInvalidEndpoint},
		{name: "query", mutate: func(value *Provider) { value.BaseURL = "https://example.com/v1?token=secret" }, category: ErrorInvalidEndpoint},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			_, err := NormalizeCustomProvider(value)
			if !IsCategory(err, test.category) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	_, err := NormalizeCustomProvider(Provider{ID: "acme", Name: "Acme", Protocol: ProtocolOpenAI, BaseURL: "https://user:super-secret@example.com/v1", AuthMode: AuthBearer, Discovery: DiscoveryNone})
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("credential-bearing endpoint error leaked secret: %v", err)
	}
}

func TestProviderStatusSeparatesSelectionConfigurationAndReadiness(t *testing.T) {
	status := ProviderStatus{ProviderID: OllamaID, Selected: true, Configured: true, Readiness: ReadinessDegraded}
	if !status.Selected || !status.Configured || status.Readiness != ReadinessDegraded {
		t.Fatalf("status = %#v", status)
	}
}

func TestRuntimeErrorsRemainTyped(t *testing.T) {
	err := NewError(ErrorRateLimited, "", "retry later")
	if !IsCategory(err, ErrorRateLimited) {
		t.Fatalf("typed error = %v", err)
	}
	if value, ok := AsError(err); !ok || value.Detail != "retry later" {
		t.Fatalf("AsError = %#v, %v", value, ok)
	}
}
