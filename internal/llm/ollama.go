package llm

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

const (
	OllamaLocalBaseURL = "http://localhost:11434/v1"
	OllamaCloudBaseURL = "https://ollama.com/v1"
)

type OllamaMode string

const (
	OllamaModeLocal OllamaMode = "local"
	OllamaModeCloud OllamaMode = "cloud"
)

type OllamaEndpointClass string

const (
	OllamaEndpointLocal  OllamaEndpointClass = "local"
	OllamaEndpointCloud  OllamaEndpointClass = "cloud"
	OllamaEndpointCustom OllamaEndpointClass = "custom"
)

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

func ClassifyOllamaEndpoint(raw string) (OllamaEndpointClass, error) {
	value, err := NormalizeEndpoint(raw)
	if err != nil {
		return "", err
	}
	switch value {
	case OllamaLocalBaseURL:
		return OllamaEndpointLocal, nil
	case OllamaCloudBaseURL:
		return OllamaEndpointCloud, nil
	default:
		return OllamaEndpointCustom, nil
	}
}

func ApplyOllamaMode(provider Provider, mode OllamaMode) (Provider, error) {
	if provider.ID != OllamaID {
		return Provider{}, NewError(ErrorInvalidProvider, "id", "Ollama mode can only be applied to the Ollama core provider")
	}
	provider.Protocol = ProtocolOpenAI
	provider.Discovery = DiscoveryOllamaTags
	provider.CoreKind = CoreOllama
	switch mode {
	case OllamaModeLocal:
		provider.BaseURL = OllamaLocalBaseURL
		provider.AuthMode = AuthNone
	case OllamaModeCloud:
		provider.BaseURL = OllamaCloudBaseURL
		provider.AuthMode = AuthBearer
	default:
		return Provider{}, NewError(ErrorInvalidProvider, "mode", "Ollama mode must be local or cloud")
	}
	return normalizeProvider(provider, true)
}

func (c *Client) discoverOllamaModels(ctx context.Context, provider Provider) ([]Model, error) {
	credential, err := c.credentialFor(ctx, provider, AuthBearer)
	if err != nil {
		return nil, err
	}
	endpoint, err := ollamaTagsURL(provider.BaseURL)
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	if credential != "" {
		headers.Set("Authorization", "Bearer "+credential)
	}
	raw, err := c.doJSON(ctx, http.MethodGet, endpoint, nil, headers)
	if err != nil {
		return nil, err
	}
	var response ollamaTagsResponse
	if err := decodeJSONResponse(raw, &response); err != nil {
		return nil, err
	}
	if len(response.Models) > MaxDiscoveredModels {
		return nil, NewError(ErrorInvalidResponse, "models", "provider returned too many models")
	}
	models := make([]Model, 0, len(response.Models))
	seen := make(map[string]struct{}, len(response.Models))
	for _, item := range response.Models {
		id := strings.TrimSpace(item.Model)
		name := strings.TrimSpace(item.Name)
		if id == "" {
			id = name
		}
		if name == "" {
			name = id
		}
		if id == "" || len(id) > MaxModelIDBytes {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model id")
		}
		if len(name) > MaxModelNameBytes {
			return nil, NewError(ErrorInvalidResponse, "models", "provider returned an invalid model name")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, Model{ID: id, Name: name})
	}
	return models, nil
}

func ollamaTagsURL(raw string) (string, error) {
	normalized, err := NormalizeEndpoint(raw)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", NewError(ErrorInvalidEndpoint, "base_url", "failed to parse Ollama endpoint")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if path == "/v1" {
		path = ""
	} else if strings.HasSuffix(path, "/v1") {
		path = strings.TrimSuffix(path, "/v1")
	}
	parsed.Path = strings.TrimSuffix(path, "/") + "/api/tags"
	parsed.RawPath = ""
	return parsed.String(), nil
}
