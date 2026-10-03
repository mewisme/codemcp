package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/integrations/caveman"
	"go.mewis.me/codemcp/internal/integrations/ponytail"
	"go.mewis.me/codemcp/internal/tunnel"
)

func Validate(cfg Config) error {
	if err := ValidateMCPTransports(cfg); err != nil {
		return err
	}
	for _, id := range cfg.Telegram.AllowedUserIDs {
		if id <= 0 {
			return fmt.Errorf("telegram allowed user IDs must be positive: %d", id)
		}
	}
	if cfg.HTTP.MCP.Port < 1 || cfg.HTTP.MCP.Port > 65535 {
		return fmt.Errorf("http.mcp.port must be between 1 and 65535: %d", cfg.HTTP.MCP.Port)
	}
	if cfg.HTTP.Admin.Enabled && (cfg.HTTP.Admin.Port < 1 || cfg.HTTP.Admin.Port > 65535) {
		return fmt.Errorf("http.admin.port must be between 1 and 65535: %d", cfg.HTTP.Admin.Port)
	}
	if cfg.HTTP.MCP.Enabled && cfg.HTTP.Admin.Enabled && cfg.HTTP.Admin.Port == cfg.HTTP.MCP.Port {
		return errors.New("http.admin.port must differ from http.mcp.port")
	}
	if _, err := NormalizeAllowDirs(cfg.Permissions.AllowDirs); err != nil {
		return err
	}
	if _, err := NormalizeShellPath(cfg.Shell.Path); err != nil {
		return err
	}
	if _, ok := ponytail.NormalizeRuntimeMode(cfg.Integrations.Ponytail.Mode); !ok {
		return fmt.Errorf("integrations.ponytail.mode must be lite, full, or ultra: %q", cfg.Integrations.Ponytail.Mode)
	}
	if _, ok := caveman.NormalizeRuntimeMode(cfg.Integrations.Caveman.Mode); !ok {
		return fmt.Errorf("integrations.caveman.mode must be lite, full, ultra, wenyan-lite, wenyan-full, or wenyan-ultra: %q", cfg.Integrations.Caveman.Mode)
	}
	if path := cfg.Integrations.RTK.Path; path != "" {
		if path != strings.TrimSpace(path) {
			return errors.New("integrations.rtk.path must not contain leading or trailing whitespace")
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("integrations.rtk.path must be absolute: %q", path)
		}
	}
	if path := cfg.Integrations.CodeGraph.Path; path != "" {
		if path != strings.TrimSpace(path) {
			return errors.New("integrations.codegraph.path must not contain leading or trailing whitespace")
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("integrations.codegraph.path must be absolute: %q", path)
		}
	}
	if path := cfg.Integrations.Browser.Path; path != "" {
		if path != strings.TrimSpace(path) {
			return errors.New("integrations.browser.path must not contain leading or trailing whitespace")
		}
		if !filepath.IsAbs(path) && !looksLikeWindowsAbsolutePath(path) {
			return fmt.Errorf("integrations.browser.path must be an absolute native or Windows path: %q", path)
		}
	}
	if cfg.Integrations.ChatGPTWeb.ConnectorName != strings.TrimSpace(cfg.Integrations.ChatGPTWeb.ConnectorName) ||
		strings.TrimSpace(cfg.Integrations.ChatGPTWeb.ConnectorName) == "" {
		return errors.New("integrations.chatgpt_web.connector_name must be non-empty without leading or trailing whitespace")
	}
	if cfg.Integrations.ChatGPTWeb.MaxAgents < 1 || cfg.Integrations.ChatGPTWeb.MaxAgents > 5 {
		return fmt.Errorf("integrations.chatgpt_web.max_agents must be between 1 and 5: %d", cfg.Integrations.ChatGPTWeb.MaxAgents)
	}
	if strings.TrimSpace(cfg.Integrations.TypeSafe.Model) == "" {
		return errors.New("integrations.typesafe.model must not be empty")
	}
	if cfg.Integrations.TypeSafe.TimeoutMS < 100 || cfg.Integrations.TypeSafe.TimeoutMS > 30000 {
		return fmt.Errorf("integrations.typesafe.timeout_ms must be between 100 and 30000: %d", cfg.Integrations.TypeSafe.TimeoutMS)
	}
	semanticApproval := cfg.Approval.Semantic
	if strings.TrimSpace(semanticApproval.Provider) == "" {
		return errors.New("approval.semantic.provider must not be empty")
	}
	if semanticApproval.TimeoutMS < 100 || semanticApproval.TimeoutMS > 10000 {
		return fmt.Errorf("approval.semantic.timeout_ms must be between 100 and 10000: %d", semanticApproval.TimeoutMS)
	}
	if semanticApproval.MinimumConfidence < 0 || semanticApproval.MinimumConfidence > 1 {
		return fmt.Errorf("approval.semantic.minimum_confidence must be between 0 and 1: %v", semanticApproval.MinimumConfidence)
	}
	if semanticApproval.FailMode != "require_approval" && semanticApproval.FailMode != "deny" {
		return errors.New("approval.semantic.fail_mode must be require_approval or deny")
	}
	for name, action := range map[string]string{
		"low_action": semanticApproval.LowAction, "medium_action": semanticApproval.MediumAction,
		"high_action": semanticApproval.HighAction, "critical_action": semanticApproval.CriticalAction,
	} {
		if action != "allow" && action != "require_approval" && action != "deny" {
			return fmt.Errorf("approval.semantic.%s must be allow, require_approval, or deny", name)
		}
	}
	switch cfg.Explain.Mode {
	case ExplainOff, ExplainManual, ExplainAuto:
	default:
		return fmt.Errorf("explain.mode must be off, manual, or auto: %q", cfg.Explain.Mode)
	}
	exposure := NormalizeExposure(cfg.HTTP.Exposure)
	switch exposure.Mode {
	case ExposureNone, ExposureAll, ExposureWildcard:
		if len(cfg.HTTP.Exposure.Interfaces) != 0 {
			return errors.New("http.exposure.interfaces must be empty unless mode is interfaces")
		}
	case ExposureInterfaces:
		if len(exposure.Interfaces) == 0 {
			return errors.New("http.exposure.mode=interfaces requires at least one interface")
		}
	default:
		return fmt.Errorf("http.exposure.mode must be none, all, 0.0.0.0, or interfaces: %q", cfg.HTTP.Exposure.Mode)
	}
	mcpUnauthenticated := cfg.HTTP.MCP.Enabled && !cfg.HTTP.MCP.Auth.Enabled
	adminUnauthenticated := cfg.HTTP.Admin.Enabled && !cfg.HTTP.Admin.Auth.Enabled
	if mcpUnauthenticated || adminUnauthenticated {
		if !cfg.HTTP.Security.AllowUnauthenticatedLoopback {
			return errors.New("disabling authentication on an enabled HTTP endpoint requires http.security.allow_unauthenticated_loopback=true; this acknowledgement is only valid with http.exposure.mode=none")
		}
		if exposure.Mode != ExposureNone {
			return errors.New("unauthenticated HTTP endpoints require http.exposure.mode=none (loopback only); network exposure cannot be combined with disabled authentication")
		}
	}
	if exposure.Mode != ExposureNone && (cfg.HTTP.MCP.Enabled || cfg.HTTP.Admin.Enabled) {
		if !cfg.HTTP.Security.AllowInsecure {
			return errors.New("non-loopback HTTP exposure requires http.security.allow_insecure=true; prefer Secure MCP Tunnel or a TLS reverse proxy")
		}
		if cfg.HTTP.MCP.Enabled && (!cfg.HTTP.MCP.Auth.Enabled || cfg.HTTP.MCP.Auth.TokenHash == "") {
			return errors.New("network exposure requires MCP authentication with a configured token; run cm auth mcp create")
		}
		if cfg.HTTP.Admin.Enabled && (!cfg.HTTP.Admin.Auth.Enabled || cfg.HTTP.Admin.Auth.TokenHash == "") {
			return errors.New("network exposure with the admin endpoint enabled requires admin authentication with a configured token; run cm auth admin create")
		}
	}
	if cfg.HTTP.MCP.Enabled && cfg.HTTP.MCP.Auth.Enabled && cfg.HTTP.MCP.Auth.TokenHash == "" {
		return errors.New("MCP auth is enabled but no token is configured; run cm auth mcp create")
	}
	if cfg.HTTP.Admin.Enabled && cfg.HTTP.Admin.Auth.Enabled && cfg.HTTP.Admin.Auth.TokenHash == "" {
		return errors.New("admin auth is enabled but no token is configured; run cm auth admin create")
	}
	if err := tunnel.ValidateConfig(cfg.Tunnel); err != nil {
		return err
	}
	return nil
}

func looksLikeWindowsAbsolutePath(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func UnauthenticatedLoopbackActive(cfg Config) bool {
	if !cfg.HTTP.Security.AllowUnauthenticatedLoopback {
		return false
	}
	return (cfg.HTTP.MCP.Enabled && !cfg.HTTP.MCP.Auth.Enabled) || (cfg.HTTP.Admin.Enabled && !cfg.HTTP.Admin.Auth.Enabled)
}

func UnauthenticatedLoopbackWarning() string {
	return "WARNING: unauthenticated loopback is active — MCP and/or Admin HTTP accept requests without credentials on loopback; re-enable authentication as soon as practical"
}

func CleartextHTTPActive(cfg Config) bool {
	return NormalizeExposure(cfg.HTTP.Exposure).Mode != ExposureNone
}

func CleartextHTTPWarning() string {
	return "WARNING: http.exposure is not none — bearer tokens and request contents travel on cleartext HTTP; CodeMCP has no built-in TLS (prefer Secure MCP Tunnel, a TLS reverse proxy, or a trusted/encrypted network)"
}

func SecurityWarnings(cfg Config) []string {
	warnings := []string{}
	if UnauthenticatedLoopbackActive(cfg) {
		warnings = append(warnings, UnauthenticatedLoopbackWarning())
	}
	if CleartextHTTPActive(cfg) {
		warnings = append(warnings, CleartextHTTPWarning())
	}
	return warnings
}

func ValidateMCPTransports(cfg Config) error {
	if !cfg.HTTP.MCP.Enabled && (!cfg.Tunnel.Enabled || !tunnel.Configured(cfg.Tunnel)) {
		return errors.New("at least one MCP transport must be usable: enable MCP HTTP (http.mcp.enabled) or fully configure the OpenAI Secure MCP Tunnel (tunnel.enabled, tunnel.id, and runtime API key)")
	}
	return nil
}

func NormalizeShellPath(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		path := strings.TrimSpace(value)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("shell path must be absolute: %q", value)
		}
		path = filepath.Clean(path)
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, path)
	}
	return result, nil
}

func NormalizeAllowDirs(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		path := strings.TrimSpace(value)
		if path == "" || !filepath.IsAbs(path) {
			return nil, fmt.Errorf("permissions allow dir must be an absolute path: %q", value)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("permissions allow dir %s: %w", path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("permissions allow dir is not a directory: %s", path)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("permissions allow dir %s: %w", path, err)
		}
		canonical = filepath.Clean(canonical)
		if _, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("duplicate permissions allow dir: %s", canonical)
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	sort.Strings(result)
	return result, nil
}
