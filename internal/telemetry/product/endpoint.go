package product

import (
	"errors"
	"net/url"
	"strings"
)

// Endpoint is injected by build tooling. Source builds intentionally leave it empty.
var Endpoint string

type EndpointMetadata struct {
	Available bool
	Host      string
	Product   string
}

func ParseEndpoint(raw string) (EndpointMetadata, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return EndpointMetadata{}, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return EndpointMetadata{}, errors.New("invalid telemetry endpoint")
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return EndpointMetadata{}, errors.New("telemetry endpoint must be an absolute HTTPS URL without userinfo, query, or fragment")
	}
	const route = "/v1/products/codemcp/events"
	if parsed.Path != route {
		return EndpointMetadata{}, errors.New("telemetry endpoint path does not match the CodeMCP product route")
	}
	return EndpointMetadata{Available: true, Host: parsed.Hostname(), Product: "codemcp"}, nil
}
