package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type ollamaRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn ollamaRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestOllamaCoreProfileModesAndEndpointClassification(t *testing.T) {
	provider := DefaultOllama()
	if provider.BaseURL != OllamaCloudBaseURL || provider.Protocol != ProtocolOpenAI || provider.AuthMode != AuthBearer || provider.Discovery != DiscoveryOllamaTags || provider.CoreKind != CoreOllama {
		t.Fatalf("Ollama defaults=%#v", provider)
	}
	class, err := ClassifyOllamaEndpoint(provider.BaseURL)
	if err != nil || class != OllamaEndpointCloud {
		t.Fatalf("cloud classification=%q err=%v", class, err)
	}

	provider.Model = "qwen3:8b"
	cloud, err := ApplyOllamaMode(provider, OllamaModeCloud)
	if err != nil {
		t.Fatal(err)
	}
	if cloud.BaseURL != OllamaCloudBaseURL || cloud.AuthMode != AuthBearer || cloud.Model != provider.Model || cloud.Discovery != DiscoveryOllamaTags {
		t.Fatalf("cloud provider=%#v", cloud)
	}
	class, err = ClassifyOllamaEndpoint(cloud.BaseURL)
	if err != nil || class != OllamaEndpointCloud {
		t.Fatalf("cloud classification=%q err=%v", class, err)
	}

	local, err := ApplyOllamaMode(cloud, OllamaModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.BaseURL != OllamaLocalBaseURL || local.AuthMode != AuthNone || local.Model != provider.Model {
		t.Fatalf("local provider=%#v", local)
	}

	class, err = ClassifyOllamaEndpoint("https://ollama.internal.example/v1")
	if err != nil || class != OllamaEndpointCustom {
		t.Fatalf("custom classification=%q err=%v", class, err)
	}
	if _, err := ApplyOllamaMode(provider, OllamaMode("invalid")); !IsCategory(err, ErrorInvalidProvider) {
		t.Fatalf("invalid mode err=%v", err)
	}
}

func TestOllamaCoreProfileRejectsMutableWireIdentityAndKnownEndpointAuthMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*Provider){
		"protocol":  func(value *Provider) { value.Protocol = ProtocolAnthropic },
		"discovery": func(value *Provider) { value.Discovery = DiscoveryOpenAIModels },
		"local_auth": func(value *Provider) {
			value.BaseURL = OllamaLocalBaseURL
			value.AuthMode = AuthBearer
		},
		"cloud_auth": func(value *Provider) {
			value.BaseURL = OllamaCloudBaseURL
			value.AuthMode = AuthNone
		},
		"api_key_auth": func(value *Provider) {
			value.BaseURL = "https://ollama.internal.example/v1"
			value.AuthMode = AuthAPIKey
		},
	} {
		t.Run(name, func(t *testing.T) {
			catalog := DefaultCatalog()
			mutate(&catalog.Providers[1])
			if _, err := NormalizeCatalog(catalog); !IsCategory(err, ErrorCoreInvariant) {
				t.Fatalf("Ollama mutation err=%v", err)
			}
		})
	}

	for _, auth := range []AuthMode{AuthNone, AuthBearer} {
		catalog := DefaultCatalog()
		catalog.Providers[1].BaseURL = "https://ollama.internal.example/v1"
		catalog.Providers[1].AuthMode = auth
		normalized, err := NormalizeCatalog(catalog)
		if err != nil {
			t.Fatalf("custom endpoint auth=%q err=%v", auth, err)
		}
		provider := normalized.Providers[1]
		if provider.Discovery != DiscoveryOllamaTags || provider.AuthMode != auth {
			t.Fatalf("custom provider=%#v", provider)
		}
	}
}

func TestOllamaTagsDiscoveryUsesNativeRouteAndPreservesNames(t *testing.T) {
	for _, test := range []struct {
		name       string
		auth       AuthMode
		credential string
	}{
		{name: "local", auth: AuthNone},
		{name: "cloud", auth: AuthBearer, credential: "ollama-cloud-key"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/tags" {
					t.Fatalf("request=%s %s", r.Method, r.URL.Path)
				}
				wantAuth := ""
				if test.auth == AuthBearer {
					wantAuth = "Bearer " + test.credential
				}
				if got := r.Header.Get("Authorization"); got != wantAuth {
					t.Fatalf("authorization=%q want=%q", got, wantAuth)
				}
				_, _ = fmt.Fprint(w, `{"models":[
					{"name":"qwen3:8b-cloud","model":"qwen3:8b-cloud","modified_at":"2026-09-15T01:02:03.123456789Z","size":5120000000,"digest":"sha256:abc","details":{"format":"gguf","family":"qwen3","families":["qwen3"],"parameter_size":"8.2B","quantization_level":"Q4_K_M"}},
					{"name":"namespace/model:latest","model":"namespace/model@sha256:abc"}
				]}`)
			}))
			defer server.Close()

			provider := DefaultOllama()
			provider.BaseURL = server.URL + "/v1"
			provider.AuthMode = test.auth
			client := NewClient(ClientOptions{Credential: func(context.Context, ProviderID) (string, error) {
				return test.credential, nil
			}})
			models, err := client.DiscoverModels(t.Context(), provider)
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 2 || models[0].ID != "qwen3:8b-cloud" || models[0].Name != "qwen3:8b-cloud" || models[1].ID != "namespace/model@sha256:abc" || models[1].Name != "namespace/model:latest" {
				t.Fatalf("models=%#v", models)
			}
			metadata := models[0].Ollama
			if models[0].ModifiedAt == nil || metadata == nil || metadata.SizeBytes == nil || *metadata.SizeBytes != 5120000000 || metadata.Digest != "sha256:abc" || metadata.Format != "gguf" || metadata.Family != "qwen3" || metadata.ParameterCount == nil || *metadata.ParameterCount != 8200000000 || metadata.QuantizationLevel != "Q4_K_M" {
				t.Fatalf("native Ollama metadata=%#v model=%#v", metadata, models[0])
			}
		})
	}
}

func TestOllamaCloudExactEndpointsAndBearerAuth(t *testing.T) {
	const credential = "ollama-cloud-key"
	provider, err := ApplyOllamaMode(DefaultOllama(), OllamaModeCloud)
	if err != nil {
		t.Fatal(err)
	}
	provider.Model = "gemma4:31b"
	var tagsSeen, chatSeen bool
	httpClient := &http.Client{Transport: ollamaRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.Header.Get("Authorization"); got != "Bearer "+credential {
			t.Fatalf("authorization=%q", got)
		}
		body := ""
		switch request.URL.String() {
		case "https://ollama.com/api/tags":
			tagsSeen = true
			body = `{"models":[{"name":"gemma4:31b","model":"gemma4:31b"}]}`
		case "https://ollama.com/v1/chat/completions":
			chatSeen = true
			body = `{"model":"gemma4:31b","choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`
		default:
			t.Fatalf("unexpected Cloud URL %q", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	client := NewClient(ClientOptions{
		HTTPClient: httpClient,
		Credential: func(context.Context, ProviderID) (string, error) { return credential, nil },
	})
	models, err := client.DiscoverModels(t.Context(), provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != provider.Model {
		t.Fatalf("models=%#v", models)
	}
	if _, err := client.Infer(t.Context(), provider, Request{Messages: []Message{{Role: RoleUser, Content: "hello"}}}); err != nil {
		t.Fatal(err)
	}
	if !tagsSeen || !chatSeen {
		t.Fatalf("Cloud calls tags=%v chat=%v", tagsSeen, chatSeen)
	}
}

func TestOllamaTagsURLRetainsCustomProxyPrefix(t *testing.T) {
	got, err := ollamaTagsURL("https://example.com/proxy/ollama/v1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/proxy/ollama/api/tags" {
		t.Fatalf("tags URL=%q", got)
	}
}
