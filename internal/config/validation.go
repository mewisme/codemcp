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
	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535: %d", cfg.Server.Port)
	}
	if cfg.Admin.Enabled && (cfg.Admin.Port < 1 || cfg.Admin.Port > 65535) {
		return fmt.Errorf("admin port must be between 1 and 65535: %d", cfg.Admin.Port)
	}
	if cfg.Server.Enabled && cfg.Admin.Enabled && cfg.Admin.Port == cfg.Server.Port {
		return errors.New("admin port must differ from server port")
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
	if strings.TrimSpace(cfg.Integrations.TypeSafe.Model) == "" {
		return errors.New("integrations.typesafe.model must not be empty")
	}
	if cfg.Integrations.TypeSafe.TimeoutMS < 100 || cfg.Integrations.TypeSafe.TimeoutMS > 30000 {
		return fmt.Errorf("integrations.typesafe.timeout_ms must be between 100 and 30000: %d", cfg.Integrations.TypeSafe.TimeoutMS)
	}
	exposure := NormalizeExposure(cfg.Server.Expose)
	switch exposure.Mode {
	case ExposureNone, ExposureAll, ExposureWildcard:
		if len(cfg.Server.Expose.Interfaces) != 0 {
			return errors.New("server expose interfaces must be empty unless mode is interfaces")
		}
	case ExposureInterfaces:
		if len(exposure.Interfaces) == 0 {
			return errors.New("server expose interfaces mode requires at least one interface")
		}
	default:
		return fmt.Errorf("server expose mode must be none, all, 0.0.0.0, or interfaces: %q", cfg.Server.Expose.Mode)
	}
	mcpUnauthenticated := cfg.Server.Enabled && !cfg.Auth.MCPEnabled
	adminUnauthenticated := cfg.Admin.Enabled && !cfg.Auth.AdminEnabled
	if mcpUnauthenticated || adminUnauthenticated {
		if !cfg.Server.AllowUnauthenticatedLoopback {
			return errors.New("disabling authentication on an enabled HTTP endpoint requires server.allow_unauthenticated_loopback=true; this acknowledgement is only valid with server.expose.mode=none")
		}
		if exposure.Mode != ExposureNone {
			return errors.New("unauthenticated HTTP endpoints require server.expose.mode=none (loopback only); network exposure cannot be combined with disabled authentication")
		}
	}
	if exposure.Mode != ExposureNone && (cfg.Server.Enabled || cfg.Admin.Enabled) {
		if !cfg.Server.AllowInsecureHTTP {
			return errors.New("non-loopback HTTP exposure requires server.allow_insecure_http=true; prefer Secure MCP Tunnel or a TLS reverse proxy")
		}
		if cfg.Server.Enabled && (!cfg.Auth.MCPEnabled || cfg.Auth.MCPTokenHash == "") {
			return errors.New("network exposure requires MCP authentication with a configured token; run cm auth mcp create")
		}
		if cfg.Admin.Enabled && (!cfg.Auth.AdminEnabled || cfg.Auth.AdminTokenHash == "") {
			return errors.New("network exposure with the admin endpoint enabled requires admin authentication with a configured token; run cm auth admin create")
		}
	}
	if cfg.Server.Enabled && cfg.Auth.MCPEnabled && cfg.Auth.MCPTokenHash == "" {
		return errors.New("MCP auth is enabled but no token is configured; run cm auth mcp create")
	}
	if cfg.Admin.Enabled && cfg.Auth.AdminEnabled && cfg.Auth.AdminTokenHash == "" {
		return errors.New("admin auth is enabled but no token is configured; run cm auth admin create")
	}
	if err := tunnel.ValidateConfig(cfg.Tunnel); err != nil {
		return err
	}
	return nil
}

func UnauthenticatedLoopbackActive(cfg Config) bool {
	if !cfg.Server.AllowUnauthenticatedLoopback {
		return false
	}
	return (cfg.Server.Enabled && !cfg.Auth.MCPEnabled) || (cfg.Admin.Enabled && !cfg.Auth.AdminEnabled)
}

func UnauthenticatedLoopbackWarning() string {
	return "WARNING: unauthenticated loopback is active — MCP and/or Admin HTTP accept requests without credentials on loopback; re-enable authentication as soon as practical"
}

func CleartextHTTPActive(cfg Config) bool {
	return NormalizeExposure(cfg.Server.Expose).Mode != ExposureNone
}

func CleartextHTTPWarning() string {
	return "WARNING: server.expose is not none — bearer tokens and request contents travel on cleartext HTTP; CodeMCP has no built-in TLS (prefer Secure MCP Tunnel, a TLS reverse proxy, or a trusted/encrypted network)"
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
	if !cfg.Server.Enabled && !cfg.Tunnel.Enabled {
		return errors.New("at least one MCP transport must be enabled: MCP HTTP (server.enabled) or OpenAI Secure MCP Tunnel (tunnel.enabled)")
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
