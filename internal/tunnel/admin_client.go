package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	tunnelclient "github.com/openai/tunnel-client"
	"github.com/openai/tunnel-client/pkg/clientinstance"
	tcconfig "github.com/openai/tunnel-client/pkg/config"
	tcadmin "github.com/openai/tunnel-client/pkg/controlplane/admin"
	"github.com/openai/tunnel-client/pkg/controlplane/apierror"
	tctransport "github.com/openai/tunnel-client/pkg/transport"
	"github.com/openai/tunnel-client/pkg/version"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const adminTunnelTimeout = 30 * time.Second

type tracedAdminTunnelClient struct {
	httpClient *http.Client
	baseURL    *url.URL
	adminKey   string
}

func adminTunnelClient(cfg Config, apiKey string) (*tracedAdminTunnelClient, error) {
	baseURL := strings.TrimSpace(cfg.ControlPlaneBaseURL)
	if baseURL == "" {
		baseURL = tunnelclient.DefaultControlPlaneBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid OpenAI tunnel control plane base URL %q", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("admin client: admin key is required")
	}
	transport, err := tctransport.CloneDefaultWithBundle(nil)
	if err != nil {
		return nil, err
	}
	return &tracedAdminTunnelClient{httpClient: &http.Client{Timeout: adminTunnelTimeout, Transport: transport}, baseURL: parsed, adminKey: apiKey}, nil
}

func (c *tracedAdminTunnelClient) CreateTunnel(ctx context.Context, req tcadmin.TunnelCreateRequest) (*tcadmin.Tunnel, error) {
	var out tcadmin.Tunnel
	if err := c.do(ctx, http.MethodPost, "/v1/tunnels", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *tracedAdminTunnelClient) GetTunnel(ctx context.Context, tunnelID string) (*tcadmin.Tunnel, error) {
	if tunnelID == "" {
		return nil, errors.New("tunnel id is required")
	}
	var out tcadmin.Tunnel
	if err := c.do(ctx, http.MethodGet, "/v1/tunnels/"+url.PathEscape(tunnelID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *tracedAdminTunnelClient) ListTunnels(ctx context.Context, organizationID, workspaceID, tenantID string) (*tcadmin.TunnelListResponse, error) {
	query := url.Values{}
	if organizationID != "" {
		query.Set("organization_id", organizationID)
	}
	if workspaceID != "" {
		query.Set("workspace_id", workspaceID)
	}
	if tenantID != "" {
		query.Set("tenant_id", tenantID)
	}
	var out tcadmin.TunnelListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/tunnels", query, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *tracedAdminTunnelClient) UpdateTunnel(ctx context.Context, tunnelID string, req tcadmin.TunnelUpdateRequest) (*tcadmin.Tunnel, error) {
	if tunnelID == "" {
		return nil, errors.New("tunnel id is required")
	}
	var out tcadmin.Tunnel
	if err := c.do(ctx, http.MethodPost, "/v1/tunnels/"+url.PathEscape(tunnelID), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *tracedAdminTunnelClient) DeleteTunnel(ctx context.Context, tunnelID string) (*tcadmin.Tunnel, error) {
	if tunnelID == "" {
		return nil, errors.New("tunnel id is required")
	}
	var out tcadmin.Tunnel
	if err := c.do(ctx, http.MethodDelete, "/v1/tunnels/"+url.PathEscape(tunnelID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *tracedAdminTunnelClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	target := tcconfig.ResolveControlPlanePath(c.baseURL, "", path)
	if len(query) > 0 {
		target.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.adminKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", version.UserAgent)
	request.Header.Set("X-Tunnel-Client-Name", version.ClientName)
	request.Header.Set("X-Tunnel-Client-Version", version.Version)
	request.Header.Set(clientinstance.HeaderName, clientinstance.ID())
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := tracepkg.DoHTTP(c.httpClient, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	requestID := response.Header.Get("x-request-id")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		info := apierror.Parse(message)
		return &tcadmin.RequestError{Method: method, Path: target.Path, StatusCode: response.StatusCode, ResponseBody: strings.TrimSpace(string(message)), RequestID: requestID, Code: info.Code, ErrorType: info.Type, Message: info.Message, Mitigation: info.Mitigation}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	switch value := out.(type) {
	case *tcadmin.Tunnel:
		if value != nil && value.RequestID == "" {
			value.RequestID = requestID
		}
	case *tcadmin.TunnelListResponse:
		if value != nil && value.RequestID == "" {
			value.RequestID = requestID
		}
	}
	return nil
}
