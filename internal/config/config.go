package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/integrations"
	"go.mewis.me/codemcp/internal/tunnel"
)

type Config struct {
	HTTP          HTTPConfig          `json:"http"`
	Permissions   PermissionsConfig   `json:"permissions"`
	Shell         ShellConfig         `json:"shell"`
	Notifications NotificationsConfig `json:"notifications"`
	Approval      ApprovalConfig      `json:"approval"`
	Telemetry     TelemetryConfig     `json:"telemetry"`
	Telegram      TelegramConfig      `json:"telegram"`
	Integrations  IntegrationsConfig  `json:"integrations"`
	Tunnel        tunnel.Config       `json:"tunnel"`
}

type TelemetryConfig struct {
	Enabled bool `json:"enabled"`
}

type TelegramConfig struct {
	Enabled        bool                      `json:"enabled"`
	AllowedUserIDs []int64                   `json:"allowed_user_ids"`
	TopicsEnabled  bool                      `json:"topics_enabled"`
	LogsMiniApp    TelegramLogsMiniAppConfig `json:"logs_mini_app"`
}

type TelegramLogsMiniAppConfig struct {
	Enabled bool `json:"enabled"`
}

type ApprovalConfig struct {
	Semantic SemanticApprovalConfig `json:"semantic"`
	Explain  ApprovalExplainConfig  `json:"explain"`
}

type ApprovalExplainMode string

const (
	ApprovalExplainOff    ApprovalExplainMode = "off"
	ApprovalExplainManual ApprovalExplainMode = "manual"
	ApprovalExplainAuto   ApprovalExplainMode = "auto"
)

type ApprovalExplainConfig struct {
	Mode ApprovalExplainMode `json:"mode"`
}

type SemanticApprovalConfig struct {
	Enabled           bool    `json:"enabled"`
	Provider          string  `json:"provider"`
	TimeoutMS         int     `json:"timeout_ms"`
	MinimumConfidence float64 `json:"minimum_confidence"`
	FailMode          string  `json:"fail_mode"`
	LowAction         string  `json:"low_action"`
	MediumAction      string  `json:"medium_action"`
	HighAction        string  `json:"high_action"`
	CriticalAction    string  `json:"critical_action"`
}

type PermissionsConfig struct {
	AllowDirs      []string `json:"allow_dirs"`
	MCPConfigRead  bool     `json:"mcp_config_read"`
	MCPConfigWrite bool     `json:"mcp_config_write"`
}

type ShellConfig struct {
	Path []string `json:"path"`
}

type NotificationsConfig struct {
	Approval   ApprovalNotificationConfig   `json:"approval"`
	Completion CompletionNotificationConfig `json:"completion"`
}

type ApprovalNotificationConfig struct {
	Enabled         bool `json:"enabled"`
	Pending         bool `json:"pending"`
	Resolved        bool `json:"resolved"`
	DesktopEnabled  bool `json:"desktop_enabled"`
	TelegramEnabled bool `json:"telegram_enabled"`
}

type CompletionNotificationConfig struct {
	Enabled         bool `json:"enabled"`
	DesktopEnabled  bool `json:"desktop_enabled"`
	TelegramEnabled bool `json:"telegram_enabled"`
}

type HTTPConfig struct {
	Exposure ExposureConfig     `json:"exposure"`
	Security HTTPSecurityConfig `json:"security"`
	MCP      MCPHTTPConfig      `json:"mcp"`
	Admin    AdminHTTPConfig    `json:"admin"`
}

type HTTPSecurityConfig struct {
	AllowInsecure                bool `json:"allow_insecure"`
	AllowUnauthenticatedLoopback bool `json:"allow_unauthenticated_loopback"`
}

type ExposureMode string

const (
	ExposureNone       ExposureMode = "none"
	ExposureAll        ExposureMode = "all"
	ExposureWildcard   ExposureMode = "0.0.0.0"
	ExposureInterfaces ExposureMode = "interfaces"
)

type ExposureConfig struct {
	Mode       ExposureMode `json:"mode"`
	Interfaces []string     `json:"interfaces"`
}

type MCPHTTPConfig struct {
	Enabled bool              `json:"enabled"`
	Port    int               `json:"port"`
	Auth    MCPHTTPAuthConfig `json:"auth"`
}

type AdminHTTPConfig struct {
	Enabled bool           `json:"enabled"`
	Port    int            `json:"port"`
	Auth    HTTPAuthConfig `json:"auth"`
}

type HTTPAuthConfig struct {
	Enabled   bool   `json:"enabled"`
	TokenHash string `json:"token_hash,omitempty"`
}

type MCPHTTPAuthConfig struct {
	Enabled      bool   `json:"enabled"`
	TokenHash    string `json:"token_hash,omitempty"`
	LegacyBearer bool   `json:"legacy_bearer"`
}

type legacyServerConfig struct {
	Enabled                      bool           `json:"enabled"`
	Port                         int            `json:"port"`
	Expose                       ExposureConfig `json:"expose"`
	AllowInsecureHTTP            bool           `json:"allow_insecure_http"`
	AllowUnauthenticatedLoopback bool           `json:"allow_unauthenticated_loopback"`
}

type legacyAdminConfig struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

type legacyAuthConfig struct {
	MCPEnabled      bool   `json:"mcp_enabled"`
	MCPLegacyBearer bool   `json:"mcp_legacy_bearer"`
	AdminEnabled    bool   `json:"admin_enabled"`
	MCPTokenHash    string `json:"mcp_token_hash,omitempty"`
	AdminTokenHash  string `json:"admin_token_hash,omitempty"`
}

type IntegrationsConfig = integrations.Config

func Default() Config {
	return Config{
		HTTP: HTTPConfig{
			Exposure: ExposureConfig{Mode: ExposureNone, Interfaces: []string{}},
			Security: HTTPSecurityConfig{},
			MCP: MCPHTTPConfig{Enabled: true, Port: 37421, Auth: MCPHTTPAuthConfig{
				Enabled: true, LegacyBearer: true,
			}},
			Admin: AdminHTTPConfig{Enabled: true, Port: 37422, Auth: HTTPAuthConfig{Enabled: true}},
		},
		Permissions: PermissionsConfig{AllowDirs: []string{}},
		Shell:       ShellConfig{Path: []string{}},
		Notifications: NotificationsConfig{
			Approval: ApprovalNotificationConfig{
				Enabled: false, Pending: true, Resolved: true, DesktopEnabled: true, TelegramEnabled: false,
			},
			Completion: CompletionNotificationConfig{Enabled: false, DesktopEnabled: true, TelegramEnabled: false},
		},
		Approval: ApprovalConfig{
			Semantic: SemanticApprovalConfig{
				Enabled: false, Provider: "typesafe", TimeoutMS: 1500, MinimumConfidence: 0.8,
				FailMode: "require_approval", LowAction: "allow", MediumAction: "require_approval",
				HighAction: "require_approval", CriticalAction: "deny",
			},
			Explain: ApprovalExplainConfig{Mode: ApprovalExplainOff},
		},
		Telemetry:    TelemetryConfig{Enabled: true},
		Telegram:     TelegramConfig{Enabled: false, AllowedUserIDs: []int64{}, TopicsEnabled: false, LogsMiniApp: TelegramLogsMiniAppConfig{Enabled: false}},
		Integrations: integrations.Default(),
		Tunnel:       tunnel.Config{Enabled: false, Admin: tunnel.AdminConfig{Enabled: true, EnabledSet: true}},
	}
}

func (value *ExposureConfig) UnmarshalJSON(data []byte) error {
	var legacy bool
	if err := json.Unmarshal(data, &legacy); err == nil {
		if legacy {
			*value = ExposureConfig{Mode: ExposureWildcard, Interfaces: []string{}}
		} else {
			*value = ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}
		}
		return nil
	}
	type exposureAlias ExposureConfig
	var decoded exposureAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("http.exposure must be a boolean or exposure object: %w", err)
	}
	*value = NormalizeExposure(ExposureConfig(decoded))
	return nil
}

func NormalizeExposure(value ExposureConfig) ExposureConfig {
	value.Mode = ExposureMode(strings.ToLower(strings.TrimSpace(string(value.Mode))))
	if value.Mode != ExposureInterfaces {
		value.Interfaces = []string{}
		return value
	}
	names := make([]string, 0, len(value.Interfaces))
	seen := map[string]struct{}{}
	for _, name := range value.Interfaces {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	value.Interfaces = names
	return value
}

func ParseExposure(raw string) (ExposureConfig, error) {
	value := strings.TrimSpace(raw)
	switch strings.ToLower(value) {
	case "all":
		return ExposureConfig{Mode: ExposureAll, Interfaces: []string{}}, nil
	case "true", "0.0.0.0", "wildcard":
		return ExposureConfig{Mode: ExposureWildcard, Interfaces: []string{}}, nil
	case "false", "none":
		return ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}, nil
	case "", "interfaces":
		return ExposureConfig{}, errors.New("server exposure must be none, all, 0.0.0.0, or a comma-separated interface list")
	}
	exposure := NormalizeExposure(ExposureConfig{Mode: ExposureInterfaces, Interfaces: strings.Split(value, ",")})
	if len(exposure.Interfaces) == 0 {
		return ExposureConfig{}, errors.New("server exposure interface list cannot be empty")
	}
	return exposure, nil
}

func ExposureEqual(left, right ExposureConfig) bool {
	left = NormalizeExposure(left)
	right = NormalizeExposure(right)
	if left.Mode != right.Mode || len(left.Interfaces) != len(right.Interfaces) {
		return false
	}
	for index := range left.Interfaces {
		if left.Interfaces[index] != right.Interfaces[index] {
			return false
		}
	}
	return true
}

func Load() (Config, error) {
	source, err := Source()
	if err != nil {
		return Config{}, err
	}
	return loadAt(source.Path, configformat.StructuredPathFrom(source.Path, "tunnel"))
}

func LoadAt(root string) (Config, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return Config{}, errors.New("config root is required")
	}
	path := configformat.StructuredPath(root, "config")
	return loadAt(path, configformat.StructuredPath(root, "tunnel"))
}

func LoadRuntime() (Config, error) {
	source, err := Source()
	if err != nil {
		return Config{}, err
	}
	return loadRuntimeAt(source.Path, configformat.StructuredPathFrom(source.Path, "tunnel"))
}

func LoadForTunnelRuntimeKeyReplacement() (Config, error) {
	return loadForTunnelSecretReplacement(tunnelSecretLoadPolicy{allowMissingRuntime: true})
}

func LoadForTunnelAdminKeyReplacement() (Config, error) {
	return loadForTunnelSecretReplacement(tunnelSecretLoadPolicy{allowMissingAdmin: true})
}

func LoadForTunnelSecretReplacement(runtimeKey, adminKey bool) (Config, error) {
	return loadForTunnelSecretReplacement(tunnelSecretLoadPolicy{allowMissingRuntime: runtimeKey, allowMissingAdmin: adminKey})
}

func loadForTunnelSecretReplacement(policy tunnelSecretLoadPolicy) (Config, error) {
	source, err := Source()
	if err != nil {
		return Config{}, err
	}
	return loadAtWithTunnelSecretPolicy(source.Path, configformat.StructuredPathFrom(source.Path, "tunnel"), policy)
}

func loadAt(configPath, secretPath string) (Config, error) {
	return loadAtWithTunnelSecretPolicy(configPath, secretPath, tunnelSecretLoadPolicy{})
}

func loadRuntimeAt(configPath, secretPath string) (Config, error) {
	return loadAtWithTunnelSecretPolicy(configPath, secretPath, tunnelSecretLoadPolicy{allowMissingRuntime: true, allowMissingAdmin: true})
}

func loadAtWithTunnelSecretPolicy(configPath, secretPath string, policy tunnelSecretLoadPolicy) (Config, error) {
	cfg := Default()
	data, _, err := readConfigFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &cfg); err != nil {
		return cfg, err
	}
	legacyHTTP, err := migrateLegacyHTTPConfig(data, &cfg)
	if err != nil {
		return cfg, err
	}
	cfg.HTTP.Exposure = NormalizeExposure(cfg.HTTP.Exposure)
	legacyRuntime, legacyAdmin := cfg.Tunnel.APIKey, cfg.Tunnel.Admin.Key
	if legacyRuntime == secretFileMarker {
		legacyRuntime = ""
	}
	if legacyAdmin == secretFileMarker {
		legacyAdmin = ""
	}
	migrateSecrets, err := loadTunnelSecretsWithPolicy(secretPath, &cfg.Tunnel, legacyRuntime, legacyAdmin, policy)
	if err != nil {
		return cfg, err
	}
	if legacyHTTP || migrateSecrets || legacyRuntime != "" || legacyAdmin != "" {
		if err := saveAt(configPath, secretPath, cfg); err != nil {
			return cfg, fmt.Errorf("migrate configuration: %w", err)
		}
	}
	return cfg, nil
}

func migrateLegacyHTTPConfig(data []byte, cfg *Config) (bool, error) {
	if cfg == nil {
		return false, errors.New("config is required")
	}
	rootAny, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return false, err
	}
	root, ok := rootAny.(map[string]any)
	if !ok {
		return false, errors.New("configuration must be an object")
	}
	_, hasHTTP := root["http"]
	hasLegacy := false
	for _, key := range []string{"server", "admin", "auth"} {
		if _, exists := root[key]; exists {
			hasLegacy = true
		}
	}
	if hasHTTP && hasLegacy {
		return false, errors.New("configuration contains both canonical http and legacy server/admin/auth roots; remove one representation before loading")
	}
	if !hasLegacy {
		return false, nil
	}
	if err := validateLegacyHTTPConfig(root); err != nil {
		return false, err
	}

	legacy := struct {
		Server legacyServerConfig `json:"server"`
		Admin  legacyAdminConfig  `json:"admin"`
		Auth   legacyAuthConfig   `json:"auth"`
	}{
		Server: legacyServerConfig{
			Enabled: cfg.HTTP.MCP.Enabled, Port: cfg.HTTP.MCP.Port, Expose: cfg.HTTP.Exposure,
			AllowInsecureHTTP: cfg.HTTP.Security.AllowInsecure, AllowUnauthenticatedLoopback: cfg.HTTP.Security.AllowUnauthenticatedLoopback,
		},
		Admin: legacyAdminConfig{Enabled: cfg.HTTP.Admin.Enabled, Port: cfg.HTTP.Admin.Port},
		Auth: legacyAuthConfig{
			MCPEnabled: cfg.HTTP.MCP.Auth.Enabled, MCPLegacyBearer: cfg.HTTP.MCP.Auth.LegacyBearer,
			AdminEnabled: cfg.HTTP.Admin.Auth.Enabled, MCPTokenHash: cfg.HTTP.MCP.Auth.TokenHash, AdminTokenHash: cfg.HTTP.Admin.Auth.TokenHash,
		},
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &legacy); err != nil {
		return false, err
	}
	if serverObject, ok := root["server"].(map[string]any); ok {
		if _, exists := serverObject["expose"]; !exists {
			if host, _ := serverObject["host"].(string); strings.TrimSpace(host) != "" {
				host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
				switch host {
				case "127.0.0.1", "::1", "localhost":
					legacy.Server.Expose = ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}
				case "0.0.0.0":
					legacy.Server.Expose = ExposureConfig{Mode: ExposureWildcard, Interfaces: []string{}}
				default:
					legacy.Server.Expose = ExposureConfig{Mode: ExposureAll, Interfaces: []string{}}
				}
			}
		}
	}
	cfg.HTTP = HTTPConfig{
		Exposure: NormalizeExposure(legacy.Server.Expose),
		Security: HTTPSecurityConfig{
			AllowInsecure: legacy.Server.AllowInsecureHTTP, AllowUnauthenticatedLoopback: legacy.Server.AllowUnauthenticatedLoopback,
		},
		MCP: MCPHTTPConfig{Enabled: legacy.Server.Enabled, Port: legacy.Server.Port, Auth: MCPHTTPAuthConfig{
			Enabled: legacy.Auth.MCPEnabled, TokenHash: legacy.Auth.MCPTokenHash, LegacyBearer: legacy.Auth.MCPLegacyBearer,
		}},
		Admin: AdminHTTPConfig{Enabled: legacy.Admin.Enabled, Port: legacy.Admin.Port, Auth: HTTPAuthConfig{
			Enabled: legacy.Auth.AdminEnabled, TokenHash: legacy.Auth.AdminTokenHash,
		}},
	}
	return true, nil
}

func validateLegacyHTTPConfig(root map[string]any) error {
	allowed := map[string]map[string]struct{}{
		"server": {
			"enabled": {}, "port": {}, "expose": {}, "host": {},
			"allow_insecure_http": {}, "allow_unauthenticated_loopback": {},
		},
		"admin": {"enabled": {}, "port": {}},
		"auth": {
			"mcp_enabled": {}, "mcp_legacy_bearer": {}, "admin_enabled": {},
			"mcp_token_hash": {}, "admin_token_hash": {},
		},
	}
	for _, rootKey := range []string{"server", "admin", "auth"} {
		raw, exists := root[rootKey]
		if !exists {
			continue
		}
		object, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("legacy %s configuration must be an object", rootKey)
		}
		unknown := make([]string, 0)
		for key := range object {
			if _, ok := allowed[rootKey][key]; !ok {
				unknown = append(unknown, key)
			}
		}
		sort.Strings(unknown)
		if len(unknown) > 0 {
			return fmt.Errorf("legacy %s configuration contains unsupported keys %q; remove or migrate them before loading", rootKey, unknown)
		}
	}
	serverObject, _ := root["server"].(map[string]any)
	if rawExposure, exists := serverObject["expose"]; exists {
		if exposureObject, ok := rawExposure.(map[string]any); ok {
			unknown := make([]string, 0)
			for key := range exposureObject {
				if key != "mode" && key != "interfaces" {
					unknown = append(unknown, key)
				}
			}
			sort.Strings(unknown)
			if len(unknown) > 0 {
				return fmt.Errorf("legacy server.expose configuration contains unsupported keys %q; remove or migrate them before loading", unknown)
			}
		}
	}
	return nil
}

func Save(cfg Config) error {
	path := DefaultPath()
	return saveAt(path, configformat.StructuredPathFrom(path, "tunnel"), cfg)
}

func SaveAt(root string, cfg Config) error {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return errors.New("config root is required")
	}
	return saveAt(configformat.StructuredPath(root, "config"), configformat.StructuredPath(root, "tunnel"), cfg)
}

func saveAt(configPath, secretPath string, cfg Config) error {
	return saveAtWithSecretSaver(configPath, secretPath, cfg, saveTunnelSecretAt)
}

func saveAtWithSecretSaver(configPath, secretPath string, cfg Config, saveSecret func(string, tunnel.Config) error) error {
	if err := ValidateMCPTransports(cfg); err != nil {
		return err
	}
	root := filepath.Dir(configPath)
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := configformat.MarkRoot(root); err != nil {
		return err
	}
	persisted := cfg
	allowDirs, err := NormalizeAllowDirs(persisted.Permissions.AllowDirs)
	if err != nil {
		return err
	}
	shellPath, err := NormalizeShellPath(persisted.Shell.Path)
	if err != nil {
		return err
	}
	persisted.Permissions.AllowDirs = allowDirs
	persisted.Shell.Path = shellPath
	persisted.HTTP.Exposure = NormalizeExposure(persisted.HTTP.Exposure)
	persisted.Tunnel.Admin.Enabled = tunnel.AdminEnabled(cfg.Tunnel)
	persisted.Tunnel.Admin.EnabledSet = true
	persisted.Tunnel.APIKey = ""
	persisted.Tunnel.Admin.Key = ""
	persisted.Tunnel.Admin.OrganizationID = ""
	persisted.Tunnel.Admin.WorkspaceID = ""
	persisted.Tunnel.Admin.TenantID = ""
	data, err := mergeConfigData(configPath, persisted, cfg)
	if err != nil {
		return err
	}
	configSnapshot, err := snapshotFile(configPath)
	if err != nil {
		return err
	}
	secretSnapshot, err := snapshotFile(secretPath)
	if err != nil {
		return err
	}
	if err := writeConfigFile(configPath, data, 0600); err != nil {
		return err
	}
	if err := saveSecret(secretPath, cfg.Tunnel); err != nil {
		return errors.Join(err, restoreSnapshot(configPath, configSnapshot), restoreSnapshot(secretPath, secretSnapshot))
	}
	return nil
}

const secretFileMarker = "<secret-file>"

func mergeConfigData(path string, persisted, runtime Config) ([]byte, error) {
	overlayData, err := configformat.Marshal(configformat.JSON, persisted)
	if err != nil {
		return nil, err
	}
	overlay, err := configformat.DecodeGeneric(configformat.JSON, overlayData)
	if err != nil {
		return nil, err
	}
	overlayRoot, ok := overlay.(map[string]any)
	if !ok {
		return nil, errors.New("configuration must encode as an object")
	}
	tunnelOverlay := ensureGenericObject(overlayRoot, "tunnel")
	tunnelOverlay["id"] = persisted.Tunnel.ID
	tunnelOverlay["control_plane_base_url"] = persisted.Tunnel.ControlPlaneBaseURL
	tunnelOverlay["organization_id"] = persisted.Tunnel.OrganizationID

	var base any = map[string]any{}
	existingData, _, readErr := readConfigFile(path)
	if readErr == nil {
		base, err = configformat.DecodeGeneric(configformat.JSON, existingData)
		if err != nil {
			return nil, fmt.Errorf("decode existing configuration for merge: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	merged, ok := configformat.MergeGeneric(base, overlayRoot).(map[string]any)
	if !ok {
		return nil, errors.New("configuration must be an object")
	}
	if existingRoot, ok := base.(map[string]any); ok {
		existingTunnel, _ := existingRoot["tunnel"].(map[string]any)
		mergedTunnel := ensureGenericObject(merged, "tunnel")
		if _, exists := existingTunnel["api_key"]; exists {
			mergedTunnel["api_key"] = secretMarkerValue(runtime.Tunnel.APIKey)
		}
	}
	delete(merged, "server")
	delete(merged, "admin")
	delete(merged, "auth")
	return configformat.EncodeGeneric(configformat.JSON, merged)
}

func ensureGenericObject(root map[string]any, key string) map[string]any {
	if current, ok := root[key].(map[string]any); ok {
		return current
	}
	current := map[string]any{}
	root[key] = current
	return current
}

func secretMarkerValue(value string) string {
	if value == "" {
		return ""
	}
	return secretFileMarker
}

type fileSnapshot struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

func snapshotFile(path string) (fileSnapshot, error) {
	data, mode, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileSnapshot{}, nil
		}
		return fileSnapshot{}, err
	}
	return fileSnapshot{exists: true, data: data, mode: mode}, nil
}

func restoreSnapshot(path string, snapshot fileSnapshot) error {
	if !snapshot.exists {
		return removeConfigFile(path)
	}
	return writeConfigFile(path, snapshot.data, snapshot.mode)
}
