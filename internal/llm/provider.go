package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	MaxProviderIDBytes   = 64
	MaxProviderNameBytes = 128
	MaxEndpointBytes     = 2048
	MaxModelIDBytes      = 256
	MaxModelNameBytes    = 256
)

type ProviderID string

const (
	OllamaID ProviderID = "ollama"
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
	CoreNone   CoreKind = ""
	CoreOllama CoreKind = "ollama"
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
	ID                       string               `json:"id"`
	Name                     string               `json:"name,omitempty"`
	CanonicalSlug            string               `json:"canonical_slug,omitempty"`
	Author                   string               `json:"author,omitempty"`
	ContextLength            int                  `json:"context_length,omitempty"`
	ContextLengthKnown       bool                 `json:"context_length_known,omitempty"`
	PromptPrice              string               `json:"prompt_price,omitempty"`
	CompletionPrice          string               `json:"completion_price,omitempty"`
	PricingKnown             bool                 `json:"pricing_known,omitempty"`
	Free                     bool                 `json:"free,omitempty"`
	FreeKnown                bool                 `json:"free_known,omitempty"`
	SupportedParameters      []string             `json:"supported_parameters,omitempty"`
	InputModalities          []string             `json:"input_modalities,omitempty"`
	OutputModalities         []string             `json:"output_modalities,omitempty"`
	ModalitiesKnown          bool                 `json:"modalities_known,omitempty"`
	Capabilities             []string             `json:"capabilities,omitempty"`
	SupportsStructuredOutput bool                 `json:"supports_structured_output,omitempty"`
	CapabilitiesKnown        bool                 `json:"capabilities_known,omitempty"`
	CreatedAt                *time.Time           `json:"created_at,omitempty"`
	ModifiedAt               *time.Time           `json:"modified_at,omitempty"`
	MaxOutputTokens          int                  `json:"max_output_tokens,omitempty"`
	MaxOutputTokensKnown     bool                 `json:"max_output_tokens_known,omitempty"`
	Ollama                   *OllamaModelMetadata `json:"ollama,omitempty"`
	Rank                     *ModelRankMetadata   `json:"rank,omitempty"`
	Recommendation           *ModelRecommendation `json:"recommendation,omitempty"`
}

type OllamaModelMetadata struct {
	SizeBytes         *int64   `json:"size_bytes,omitempty"`
	Digest            string   `json:"digest,omitempty"`
	Format            string   `json:"format,omitempty"`
	Family            string   `json:"family,omitempty"`
	Families          []string `json:"families,omitempty"`
	ParameterSize     string   `json:"parameter_size,omitempty"`
	ParameterCount    *int64   `json:"parameter_count,omitempty"`
	QuantizationLevel string   `json:"quantization_level,omitempty"`
}

type ModelRankMetadata struct {
	Position  int    `json:"position"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Basis     string `json:"basis"`
	Window    string `json:"window,omitempty"`
	Freshness string `json:"freshness,omitempty"`
	Value     string `json:"value,omitempty"`
}

type ModelRecommendation struct {
	Position  int     `json:"position"`
	Task      string  `json:"task"`
	Source    string  `json:"source"`
	Basis     string  `json:"basis"`
	Freshness string  `json:"freshness,omitempty"`
	Share     float64 `json:"share,omitempty"`
}

type ModelEnrichmentRequest struct {
	Rank         string
	RankWindow   string
	RecommendFor string
}

type ModelEnrichmentResult struct {
	Ranks                   map[string]ModelRankMetadata
	Recommendations         map[string]ModelRecommendation
	RankSource              string
	RankWindow              string
	RankBasis               string
	RankFreshness           string
	RecommendationSource    string
	RecommendationBasis     string
	RecommendationFreshness string
}

type InferenceClient interface {
	Infer(context.Context, Provider, Request) (Result, error)
}

type ModelDiscoverer interface {
	DiscoverModels(context.Context, Provider) ([]Model, error)
}

type ModelEnricher interface {
	EnrichModels(context.Context, Provider, ModelEnrichmentRequest) (ModelEnrichmentResult, error)
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
	return id == OllamaID
}

func DefaultOllama() Provider {
	return Provider{
		ID:        OllamaID,
		Name:      "Ollama",
		Protocol:  ProtocolOpenAI,
		BaseURL:   OllamaCloudBaseURL,
		AuthMode:  AuthBearer,
		Discovery: DiscoveryOllamaTags,
		CoreKind:  CoreOllama,
	}
}

func DefaultCatalog() Catalog {
	return Catalog{
		ActiveProvider: OllamaID,
		Providers:      []Provider{DefaultOllama()},
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
	for _, core := range []ProviderID{OllamaID} {
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
	if value.ID == OllamaID {
		if value.Protocol != ProtocolOpenAI {
			return Provider{}, NewError(ErrorCoreInvariant, "protocol", "Ollama must use the OpenAI-compatible protocol")
		}
		if value.Discovery != DiscoveryOllamaTags {
			return Provider{}, NewError(ErrorCoreInvariant, "discovery", "Ollama must use native tag discovery")
		}
		if value.AuthMode != AuthNone && value.AuthMode != AuthBearer {
			return Provider{}, NewError(ErrorCoreInvariant, "auth_mode", "Ollama supports no authentication or bearer authentication")
		}
		endpointClass, err := ClassifyOllamaEndpoint(value.BaseURL)
		if err != nil {
			return Provider{}, err
		}
		switch endpointClass {
		case OllamaEndpointLocal:
			if value.AuthMode != AuthNone {
				return Provider{}, NewError(ErrorCoreInvariant, "auth_mode", "local Ollama must not require a credential")
			}
		case OllamaEndpointCloud:
			if value.AuthMode != AuthBearer {
				return Provider{}, NewError(ErrorCoreInvariant, "auth_mode", "Ollama Cloud requires bearer authentication")
			}
		}
		value.Capabilities = nil
	}
	return value, nil
}

func coreIdentity(id ProviderID) (CoreKind, string) {
	switch id {
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
