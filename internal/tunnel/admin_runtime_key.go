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

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const runtimeServiceAccountName = "codemcp tunnel runtime"

var runtimeKeyScopes = []string{"api.organization.tunnel.read", "api.organization.tunnel.use"}

type AdminProject struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

type GeneratedRuntimeKey struct {
	ProjectID        string
	ServiceAccountID string
	KeyID            string
	Value            string
}

type adminProjectPage struct {
	Data    []AdminProject `json:"data"`
	HasMore bool           `json:"has_more"`
	LastID  string         `json:"last_id,omitempty"`
	Next    string         `json:"next,omitempty"`
}

type adminServiceAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type adminServiceAccountPage struct {
	Data    []adminServiceAccount `json:"data"`
	HasMore bool                  `json:"has_more"`
	LastID  string                `json:"last_id,omitempty"`
	Next    string                `json:"next,omitempty"`
}

type adminRuntimeKeyResponse struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

func ListAdminProjects(ctx context.Context, cfg Config) ([]AdminProject, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin.projects.list", "Listing OpenAI projects for tunnel runtime key generation")
	if strings.TrimSpace(cfg.Admin.Key) == "" {
		err := errors.New("OpenAI admin key is not configured")
		span.FailMessage("OpenAI project listing failed", err)
		return nil, err
	}
	var page adminProjectPage
	if err := adminPlatformRequest(ctx, cfg, http.MethodGet, "/v1/organization/projects?limit=100&include_archived=false", nil, &page); err != nil {
		span.FailMessage("OpenAI project listing failed", errors.New("OpenAI project request failed"))
		return nil, err
	}
	projects := make([]AdminProject, 0, len(page.Data))
	for _, project := range page.Data {
		if project.Status == "" || strings.EqualFold(project.Status, "active") {
			projects = append(projects, project)
		}
	}
	span.EndMessage("OpenAI projects listed", tracepkg.Int("reported_count", len(page.Data)), tracepkg.Int("active_count", len(projects)), tracepkg.Bool("has_more", page.HasMore))
	return projects, nil
}

func GenerateRuntimeKey(ctx context.Context, cfg Config, projectID string) (GeneratedRuntimeKey, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	projectID = strings.TrimSpace(projectID)
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.runtime-key.generate", "Generating tunnel runtime API key", tracepkg.Bool("automatic_generation_requested", true), tracepkg.Bool("project_explicit", projectID != ""), tracepkg.Any("scopes", append([]string(nil), runtimeKeyScopes...)))
	selectionStrategy := "explicit"
	if projectID == "" {
		projects, err := ListAdminProjects(ctx, cfg)
		if err != nil {
			span.FailMessage("Tunnel runtime key project discovery failed", errors.New("OpenAI project discovery failed"))
			return GeneratedRuntimeKey{}, fmt.Errorf("list OpenAI projects for runtime key generation: %w", err)
		}
		projectID, err = selectRuntimeKeyProject(projects)
		if err != nil {
			span.FailMessage("Tunnel runtime key project selection failed", err, tracepkg.Int("active_project_count", len(projects)))
			return GeneratedRuntimeKey{}, err
		}
		selectionStrategy = runtimeKeyProjectSelectionStrategy(projects, projectID)
	}
	tracepkg.Emit(ctx, "TUNNEL", "tunnel.runtime-key.project-selected", "Selected OpenAI project for tunnel runtime key", tracepkg.String("project_id", projectID), tracepkg.String("selection_strategy", selectionStrategy))
	account, err := findOrCreateRuntimeServiceAccount(ctx, cfg, projectID)
	if err != nil {
		span.FailMessage("Tunnel runtime service account preparation failed", errors.New("OpenAI runtime service account preparation failed"), tracepkg.String("project_id", projectID))
		return GeneratedRuntimeKey{}, err
	}
	path := fmt.Sprintf("/v1/organization/projects/%s/service_accounts/%s/api_keys", url.PathEscape(projectID), url.PathEscape(account.ID))
	body := map[string]any{"name": runtimeServiceAccountName, "scopes": append([]string(nil), runtimeKeyScopes...)}
	var response adminRuntimeKeyResponse
	if err := adminPlatformRequest(ctx, cfg, http.MethodPost, path, body, &response); err != nil {
		span.FailMessage("Tunnel runtime API key creation failed", errors.New("OpenAI runtime API key request failed"), tracepkg.String("project_id", projectID), tracepkg.String("service_account_id", account.ID))
		return GeneratedRuntimeKey{}, fmt.Errorf("create OpenAI runtime API key: %w", err)
	}
	if strings.TrimSpace(response.Value) == "" {
		err := errors.New("OpenAI runtime API key response did not include the key value")
		span.FailMessage("Tunnel runtime API key response invalid", err, tracepkg.String("project_id", projectID), tracepkg.String("service_account_id", account.ID), tracepkg.String("key_id", response.ID))
		return GeneratedRuntimeKey{}, err
	}
	result := GeneratedRuntimeKey{ProjectID: projectID, ServiceAccountID: account.ID, KeyID: response.ID, Value: response.Value}
	span.EndMessage("Tunnel runtime API key generated", tracepkg.Bool("generated", true), tracepkg.String("project_id", projectID), tracepkg.String("selection_strategy", selectionStrategy), tracepkg.String("service_account_id", account.ID), tracepkg.String("key_id", response.ID), tracepkg.Any("scopes", append([]string(nil), runtimeKeyScopes...)))
	return result, nil
}

func runtimeKeyProjectSelectionStrategy(projects []AdminProject, selected string) string {
	if len(projects) == 1 {
		return "single_active"
	}
	for _, project := range projects {
		if project.ID == selected && strings.EqualFold(strings.TrimSpace(project.Name), "default project") {
			return "default_project"
		}
	}
	return "resolved"
}

func selectRuntimeKeyProject(projects []AdminProject) (string, error) {
	if len(projects) == 0 {
		return "", errors.New("no active OpenAI project is available for runtime key generation")
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	for _, project := range projects {
		if strings.EqualFold(strings.TrimSpace(project.Name), "default project") {
			return project.ID, nil
		}
	}
	return "", errors.New("multiple active OpenAI projects are available; choose a project for runtime key generation")
}

func findOrCreateRuntimeServiceAccount(ctx context.Context, cfg Config, projectID string) (adminServiceAccount, error) {
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.runtime-key.service-account", "Preparing tunnel runtime service account", tracepkg.String("project_id", projectID))
	path := fmt.Sprintf("/v1/organization/projects/%s/service_accounts?limit=100", url.PathEscape(projectID))
	var page adminServiceAccountPage
	if err := adminPlatformRequest(ctx, cfg, http.MethodGet, path, nil, &page); err != nil {
		span.FailMessage("Tunnel runtime service account listing failed", errors.New("OpenAI service account listing failed"))
		return adminServiceAccount{}, fmt.Errorf("list OpenAI project service accounts: %w", err)
	}
	for _, account := range page.Data {
		if strings.EqualFold(strings.TrimSpace(account.Name), runtimeServiceAccountName) {
			span.EndMessage("Tunnel runtime service account reused", tracepkg.Bool("reused", true), tracepkg.String("service_account_id", account.ID), tracepkg.Int("service_account_count", len(page.Data)))
			return account, nil
		}
	}
	var account adminServiceAccount
	body := map[string]any{"name": runtimeServiceAccountName, "create_service_account_only": true}
	if err := adminPlatformRequest(ctx, cfg, http.MethodPost, fmt.Sprintf("/v1/organization/projects/%s/service_accounts", url.PathEscape(projectID)), body, &account); err != nil {
		span.FailMessage("Tunnel runtime service account creation failed", errors.New("OpenAI service account creation failed"))
		return adminServiceAccount{}, fmt.Errorf("create OpenAI runtime service account: %w", err)
	}
	if strings.TrimSpace(account.ID) == "" {
		err := errors.New("OpenAI service account response did not include an id")
		span.FailMessage("Tunnel runtime service account response invalid", err)
		return adminServiceAccount{}, err
	}
	span.EndMessage("Tunnel runtime service account created", tracepkg.Bool("reused", false), tracepkg.String("service_account_id", account.ID), tracepkg.Int("service_account_count", len(page.Data)))
	return account, nil
}

func adminPlatformRequest(ctx context.Context, cfg Config, method, path string, body any, output any) error {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ControlPlaneBaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(tunnelclient.DefaultControlPlaneBaseURL, "/")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid OpenAI API base URL %q", baseURL)
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	target := baseURL + path
	span := tracepkg.Start(ctx, "TUNNEL", "tunnel.admin-platform.request", "OpenAI Admin API request", tracepkg.String("method", method), tracepkg.URL("url", target), tracepkg.Bool("request_body", body != nil))
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		span.FailMessage("OpenAI Admin API request construction failed", err)
		return err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.Admin.Key))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	response, err := tracepkg.DoHTTP(client, request)
	if err != nil {
		span.FailMessage("OpenAI Admin API request failed", errors.New("OpenAI Admin API HTTP request failed"))
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		span.FailMessage("OpenAI Admin API response read failed", errors.New("OpenAI Admin API response read failed"), tracepkg.Int("status", response.StatusCode))
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = response.Status
		}
		span.FailMessage("OpenAI Admin API request rejected", errors.New("OpenAI Admin API request rejected"), tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(data))))
		return fmt.Errorf("OpenAI Admin API %s %s: %s", method, path, message)
	}
	if output == nil || len(data) == 0 {
		span.EndMessage("OpenAI Admin API request completed", tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(data))), tracepkg.Bool("decoded", false))
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		span.FailMessage("OpenAI Admin API response decode failed", errors.New("OpenAI Admin API response decode failed"), tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(data))))
		return fmt.Errorf("decode OpenAI Admin API response: %w", err)
	}
	span.EndMessage("OpenAI Admin API request completed", tracepkg.Int("status", response.StatusCode), tracepkg.Int64("response_bytes", int64(len(data))), tracepkg.Bool("decoded", true))
	return nil
}
