package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	MaxProviderIDBytes   = 64
	MaxProviderNameBytes = 128
	MaxEndpointBytes     = 2048
	MaxModelIDBytes      = 256
	MaxModelNameBytes    = 256
)

const (
	OpenRouterBaseURL      = "https://openrouter.ai/api/v1"
	OpenRouterDefaultModel = "openrouter/free"
)

type ProviderID string

const (
	OpenRouterID ProviderID = "openrouter"
	OllamaID     ProviderID = "ollama"
)

type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
)

type AuthMode string

const (
	AuthNone   AuthMode = "none"
	AuthBearer AuthMode = "bearer"
	AuthAPIKey AuthMode = "x-api-key"
)

type DiscoveryMode string

const (
	DiscoveryNone         DiscoveryMode = "none"
	DiscoveryOpenAIModels DiscoveryMode = "openai-models"
	DiscoveryOllamaTags   DiscoveryMode = "ollama-tags"
)

type CoreKind string

const (
	CoreNone       CoreKind = ""
	CoreOpenRouter CoreKind = "openrouter"
	CoreOllama     CoreKind = "ollama"
)

type Readiness string

const (
	ReadinessUnknown     Readiness = "unknown"
	ReadinessReady       Readiness = "ready"
	ReadinessUnavailable Readiness = "unavailable"
	ReadinessDegraded    Readiness = "degraded"
)

type ProviderCapabilities struct {
	StructuredOutput bool `json:"structured_output,omitempty"`
}

type Provider struct {
	ID           ProviderID            `json:"id"`
	Name         string                `json:"name"`
	Protocol     Protocol              `json:"protocol"`
	BaseURL      string                `json:"base_url"`
	Model        string                `json:"model,omitempty"`
	AuthMode     AuthMode              `json:"auth_mode"`
	Discovery    DiscoveryMode         `json:"discovery"`
	CoreKind     CoreKind              `json:"core_kind,omitempty"`
	Capabilities *ProviderCapabilities `json:"capabilities,omitempty"`
}

type Catalog struct {
	ActiveProvider ProviderID `json:"active_provider"`
	Providers      []Provider `json:"providers"`
}

type ProviderStatus struct {
	ProviderID ProviderID `json:"provider_id"`
	Selected   bool       `json:"selected"`
	Configured bool       `json:"configured"`
	Readiness  Readiness  `json:"readiness"`
	Reason     string     `json:"reason,omitempty"`
}

type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

type Message struct {
	Role    MessageRole `json:"role"`
	Content string      `json:"content"`
}

type Request struct {
	Instructions    string          `json:"instructions,omitempty"`
	Messages        []Message       `json:"messages"`
	ResponseSchema  json.RawMessage `json:"response_schema,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Result struct {
	Text         string          `json:"text"`
	Structured   json.RawMessage `json:"structured,omitempty"`
	ProviderID   ProviderID      `json:"provider_id"`
	Model        string          `json:"model"`
	Usage        *Usage          `json:"usage,omitempty"`
	FinishReason string          `json:"finish_reason,omitempty"`
}

type Model struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name,omitempty"`
	ContextLength            int      `json:"context_length,omitempty"`
	PromptPrice              string   `json:"prompt_price,omitempty"`
	CompletionPrice          string   `json:"completion_price,omitempty"`
	Free                     bool     `json:"free,omitempty"`
	SupportedParameters      []string `json:"supported_parameters,omitempty"`
	SupportsStructuredOutput bool     `json:"supports_structured_output,omitempty"`
}

type InferenceClient interface {
	Infer(context.Context, Provider, Request) (Result, error)
}

type ModelDiscoverer interface {
	DiscoverModels(context.Context, Provider) ([]Model, error)
}

var providerIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

func NormalizeProviderID(raw string) (ProviderID, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || len(value) > MaxProviderIDBytes || !providerIDPattern.MatchString(value) {
		return "", NewError(ErrorInvalidID, "id", "provider id must use 1-64 lowercase letters, digits, '.', '_' or '-' and start/end with a letter or digit")
	}
	return ProviderID(value), nil
}

func IsCoreProvider(id ProviderID) bool {
	return id == OpenRouterID || id == OllamaID
}

func DefaultOpenRouter() Provider {
	return Provider{
		ID:           OpenRouterID,
		Name:         "OpenRouter",
		Protocol:     ProtocolOpenAI,
		BaseURL:      OpenRouterBaseURL,
		Model:        OpenRouterDefaultModel,
		AuthMode:     AuthBearer,
		Discovery:    DiscoveryOpenAIModels,
		CoreKind:     CoreOpenRouter,
		Capabilities: &ProviderCapabilities{StructuredOutput: true},
	}
}

func DefaultOllama() Provider {
	return Provider{
		ID:        OllamaID,
		Name:      "Ollama",
		Protocol:  ProtocolOpenAI,
		BaseURL:   "http://localhost:11434/v1",
		AuthMode:  AuthNone,
		Discovery: DiscoveryOllamaTags,
		CoreKind:  CoreOllama,
	}
}

func DefaultCatalog() Catalog {
	return Catalog{
		ActiveProvider: OpenRouterID,
		Providers:      []Provider{DefaultOpenRouter(), DefaultOllama()},
	}
}

func NormalizeCustomProvider(value Provider) (Provider, error) {
	id, err := NormalizeProviderID(string(value.ID))
	if err != nil {
		return Provider{}, err
	}
	if IsCoreProvider(id) {
		return Provider{}, NewError(ErrorReservedID, "id", fmt.Sprintf("provider id %q is reserved", id))
	}
	value.ID = id
	return normalizeProvider(value, false)
}

func NormalizeCatalog(value Catalog) (Catalog, error) {
	active, err := NormalizeProviderID(string(value.ActiveProvider))
	if err != nil {
		return Catalog{}, NewError(ErrorInvalidActive, "active_provider", err.Error())
	}

	providers := make([]Provider, 0, len(value.Providers))
	seen := make(map[ProviderID]struct{}, len(value.Providers))
	for _, provider := range value.Providers {
		provider, err = normalizeProvider(provider, true)
		if err != nil {
			return Catalog{}, err
		}
		if _, exists := seen[provider.ID]; exists {
			return Catalog{}, NewError(ErrorDuplicateID, "providers", fmt.Sprintf("duplicate provider id %q", provider.ID))
		}
		seen[provider.ID] = struct{}{}
		providers = append(providers, provider)
	}
	for _, core := range []ProviderID{OpenRouterID, OllamaID} {
		if _, exists := seen[core]; !exists {
			return Catalog{}, NewError(ErrorMissingCore, "providers", fmt.Sprintf("core provider %q is required", core))
		}
	}
	if _, exists := seen[active]; !exists {
		return Catalog{}, NewError(ErrorInvalidActive, "active_provider", fmt.Sprintf("provider %q is not registered", active))
	}
	value.ActiveProvider = active
	value.Providers = providers
	return value, nil
}

func ValidateProviderRemoval(catalog Catalog, rawID string) error {
	value, err := NormalizeCatalog(catalog)
	if err != nil {
		return err
	}
	id, err := NormalizeProviderID(rawID)
	if err != nil {
		return err
	}
	if IsCoreProvider(id) {
		return NewError(ErrorCoreInvariant, "id", fmt.Sprintf("core provider %q cannot be removed", id))
	}
	if id == value.ActiveProvider {
		return NewError(ErrorActiveRemoval, "id", fmt.Sprintf("active provider %q must be deselected before removal", id))
	}
	for _, provider := range value.Providers {
		if provider.ID == id {
			return nil
		}
	}
	return NewError(ErrorProviderNotFound, "id", fmt.Sprintf("provider %q is not registered", id))
}

func NormalizeEndpoint(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > MaxEndpointBytes {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "endpoint is required and must be at most 2048 bytes")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "endpoint must not contain credentials")
	}
	if parsed.Fragment != "" {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "endpoint must not contain a fragment")
	}
	if parsed.RawQuery != "" {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "endpoint must not contain query parameters or credentials")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizeProvider(value Provider, allowCore bool) (Provider, error) {
	id, err := NormalizeProviderID(string(value.ID))
	if err != nil {
		return Provider{}, err
	}
	value.ID = id

	expectedKind, expectedName := coreIdentity(id)
	if expectedKind != CoreNone {
		if !allowCore {
			return Provider{}, NewError(ErrorReservedID, "id", fmt.Sprintf("provider id %q is reserved", id))
		}
		if value.CoreKind != CoreNone && value.CoreKind != expectedKind {
			return Provider{}, NewError(ErrorCoreInvariant, "core_kind", fmt.Sprintf("provider %q must remain %q", id, expectedKind))
		}
		value.CoreKind = expectedKind
		if strings.TrimSpace(value.Name) == "" {
			value.Name = expectedName
		} else if strings.TrimSpace(value.Name) != expectedName {
			return Provider{}, NewError(ErrorCoreInvariant, "name", fmt.Sprintf("core provider %q cannot be renamed", id))
		}
	} else if value.CoreKind != CoreNone {
		return Provider{}, NewError(ErrorCoreInvariant, "core_kind", "custom providers cannot claim a core identity")
	}

	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		value.Name = string(id)
	}
	if len(value.Name) > MaxProviderNameBytes {
		return Provider{}, NewError(ErrorInvalidProvider, "name", "provider name must be at most 128 bytes")
	}
	if !validProtocol(value.Protocol) {
		return Provider{}, NewError(ErrorInvalidProtocol, "protocol", fmt.Sprintf("unsupported protocol %q", value.Protocol))
	}
	if !validAuthMode(value.AuthMode) {
		return Provider{}, NewError(ErrorInvalidAuth, "auth_mode", fmt.Sprintf("unsupported auth mode %q", value.AuthMode))
	}
	if !validDiscoveryMode(value.Discovery) {
		return Provider{}, NewError(ErrorInvalidDiscovery, "discovery", fmt.Sprintf("unsupported discovery mode %q", value.Discovery))
	}
	value.BaseURL, err = NormalizeEndpoint(value.BaseURL)
	if err != nil {
		return Provider{}, err
	}
	value.Model = strings.TrimSpace(value.Model)
	if len(value.Model) > MaxModelIDBytes {
		return Provider{}, NewError(ErrorInvalidProvider, "model", "model id must be at most 256 bytes")
	}
	if value.ID == OpenRouterID {
		if value.Protocol != ProtocolOpenAI {
			return Provider{}, NewError(ErrorCoreInvariant, "protocol", "OpenRouter must use the OpenAI-compatible protocol")
		}
		if value.AuthMode != AuthBearer {
			return Provider{}, NewError(ErrorCoreInvariant, "auth_mode", "OpenRouter must use bearer authentication")
		}
		if value.Discovery != DiscoveryOpenAIModels {
			return Provider{}, NewError(ErrorCoreInvariant, "discovery", "OpenRouter must use OpenAI-compatible model discovery")
		}
		value.Capabilities = &ProviderCapabilities{StructuredOutput: true}
	}
	return value, nil
}

func coreIdentity(id ProviderID) (CoreKind, string) {
	switch id {
	case OpenRouterID:
		return CoreOpenRouter, "OpenRouter"
	case OllamaID:
		return CoreOllama, "Ollama"
	default:
		return CoreNone, ""
	}
}

func validProtocol(value Protocol) bool {
	return value == ProtocolOpenAI || value == ProtocolAnthropic
}

func validAuthMode(value AuthMode) bool {
	return value == AuthNone || value == AuthBearer || value == AuthAPIKey
}

func validDiscoveryMode(value DiscoveryMode) bool {
	return value == DiscoveryNone || value == DiscoveryOpenAIModels || value == DiscoveryOllamaTags
}
