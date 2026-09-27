package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxProbeResponseBody = 64 << 10

type ProbeErrorCategory string

const (
	ProbeErrorUnauthorized ProbeErrorCategory = "unauthorized"
	ProbeErrorInvalid      ProbeErrorCategory = "invalid_request"
	ProbeErrorRateLimited  ProbeErrorCategory = "rate_limited"
	ProbeErrorOverloaded   ProbeErrorCategory = "overloaded"
	ProbeErrorHTTP         ProbeErrorCategory = "http_error"
	ProbeErrorNetwork      ProbeErrorCategory = "network_error"
	ProbeErrorResponse     ProbeErrorCategory = "invalid_response"
)

type ProbeResult struct {
	HTTPStatus     int      `json:"http_status"`
	Model          string   `json:"model"`
	ModelAvailable bool     `json:"model_available"`
	Models         []string `json:"models,omitempty"`
}

type ProbeError struct {
	Category   ProbeErrorCategory `json:"category"`
	HTTPStatus int                `json:"http_status,omitempty"`
}

func (e *ProbeError) Error() string {
	if e == nil {
		return ""
	}
	if e.HTTPStatus > 0 {
		return fmt.Sprintf("typesafe probe failed: %s (http %d)", e.Category, e.HTTPStatus)
	}
	return "typesafe probe failed: " + string(e.Category)
}

type ProbeOptions struct {
	Client  *http.Client
	BaseURL string
}

func Probe(ctx context.Context, apiKey, model string, timeout time.Duration, options ProbeOptions) (ProbeResult, error) {
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" {
		return ProbeResult{}, errors.New("typesafe API key is not configured")
	}
	if model == "" {
		return ProbeResult{}, errors.New("typesafe model is required")
	}
	if timeout <= 0 {
		return ProbeResult{}, errors.New("typesafe probe timeout must be positive")
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return ProbeResult{}, errors.New("typesafe probe base URL is invalid")
	}
	if options.BaseURL == "" && (parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "api.typesafe.ai") || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "") {
		return ProbeResult{}, errors.New("typesafe production base URL is invalid")
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + ModelsPath
	client := options.Client
	client = cloneProbeClient(client, options.BaseURL == "")

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProbeResult{}, &ProbeError{Category: ProbeErrorNetwork}
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return ProbeResult{}, &ProbeError{Category: ProbeErrorNetwork}
	}
	defer response.Body.Close()

	result := ProbeResult{HTTPStatus: response.StatusCode, Model: model}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeResponseBody+1))
		return result, &ProbeError{Category: probeCategoryForStatus(response.StatusCode), HTTPStatus: response.StatusCode}
	}
	if response.ContentLength > maxProbeResponseBody {
		return result, &ProbeError{Category: ProbeErrorResponse, HTTPStatus: response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxProbeResponseBody+1))
	if err != nil || len(data) > maxProbeResponseBody {
		return result, &ProbeError{Category: ProbeErrorResponse, HTTPStatus: response.StatusCode}
	}
	var decoded ModelsResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return result, &ProbeError{Category: ProbeErrorResponse, HTTPStatus: response.StatusCode}
	}
	if len(decoded.Models) == 0 {
		return result, &ProbeError{Category: ProbeErrorResponse, HTTPStatus: response.StatusCode}
	}
	result.Models = make([]string, 0, len(decoded.Models))
	for _, card := range decoded.Models {
		name := strings.TrimSpace(card.Name)
		if name == "" {
			return result, &ProbeError{Category: ProbeErrorResponse, HTTPStatus: response.StatusCode}
		}
		result.Models = append(result.Models, name)
		if name == model {
			result.ModelAvailable = true
		}
	}
	if !result.ModelAvailable {
		return result, &ProbeError{Category: ProbeErrorInvalid, HTTPStatus: response.StatusCode}
	}
	return result, nil
}

func cloneProbeClient(source *http.Client, production bool) *http.Client {
	client := &http.Client{}
	if source != nil {
		*client = *source
	}
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 0 && !strings.EqualFold(request.URL.Hostname(), via[0].URL.Hostname()) {
			return errors.New("typesafe cross-host redirect rejected")
		}
		if production && (request.URL.Scheme != "https" ||
			!strings.EqualFold(request.URL.Hostname(), "api.typesafe.ai") ||
			request.URL.Path != ModelsPath || request.URL.RawQuery != "" || request.URL.Fragment != "") {
			return errors.New("typesafe production redirect rejected")
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("typesafe redirect limit exceeded")
		}
		return nil
	}
	return client
}

func probeCategoryForStatus(status int) ProbeErrorCategory {
	switch status {
	case http.StatusUnauthorized:
		return ProbeErrorUnauthorized
	case http.StatusUnprocessableEntity:
		return ProbeErrorInvalid
	case http.StatusTooManyRequests:
		return ProbeErrorRateLimited
	case 529:
		return ProbeErrorOverloaded
	default:
		return ProbeErrorHTTP
	}
}
