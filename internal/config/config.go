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
	Server        ServerConfig        `json:"server"`
	Admin         AdminConfig         `json:"admin"`
	Auth          AuthConfig          `json:"auth"`
	Permissions   PermissionsConfig   `json:"permissions"`
	Shell         ShellConfig         `json:"shell"`
	Notifications NotificationsConfig `json:"notifications"`
	Integrations  IntegrationsConfig  `json:"integrations"`
	Tunnel        tunnel.Config       `json:"tunnel"`
}

type PermissionsConfig struct {
	AllowDirs []string `json:"allow_dirs"`
}

type ShellConfig struct {
	Path []string `json:"path"`
}

type NotificationsConfig struct {
	Approval ApprovalNotificationConfig `json:"approval"`
}

type ApprovalNotificationConfig struct {
	Enabled         bool `json:"enabled"`
	Pending         bool `json:"pending"`
	Resolved        bool `json:"resolved"`
	DesktopEnabled  bool `json:"desktop_enabled"`
	TelegramEnabled bool `json:"telegram_enabled"`
}

type ServerConfig struct {
	Enabled                      bool           `json:"enabled"`
	Port                         int            `json:"port"`
	Expose                       ExposureConfig `json:"expose"`
	AllowInsecureHTTP            bool           `json:"allow_insecure_http"`
	AllowUnauthenticatedLoopback bool           `json:"allow_unauthenticated_loopback"`
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

type AdminConfig struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

type AuthConfig struct {
	MCPEnabled      bool   `json:"mcp_enabled"`
	MCPLegacyBearer bool   `json:"mcp_legacy_bearer"`
	AdminEnabled    bool   `json:"admin_enabled"`
	MCPTokenHash    string `json:"mcp_token_hash,omitempty"`
	AdminTokenHash  string `json:"admin_token_hash,omitempty"`
}

type IntegrationsConfig = integrations.Config

func Default() Config {
	return Config{
		Server:      ServerConfig{Enabled: true, Port: 37421, Expose: ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}},
		Admin:       AdminConfig{Enabled: true, Port: 37422},
		Auth:        AuthConfig{MCPEnabled: true, MCPLegacyBearer: true, AdminEnabled: true},
		Permissions: PermissionsConfig{AllowDirs: []string{}},
		Shell:       ShellConfig{Path: []string{}},
		Notifications: NotificationsConfig{Approval: ApprovalNotificationConfig{
			Enabled: false, Pending: true, Resolved: true, DesktopEnabled: true, TelegramEnabled: false,
		}},
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
		return fmt.Errorf("server.expose must be a boolean or exposure object: %w", err)
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
	cfg.Server.Expose = NormalizeExposure(cfg.Server.Expose)
	if err := migrateLegacyServerConfig(configPath, data, &cfg); err != nil {
		return cfg, err
	}
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
	if migrateSecrets || legacyRuntime != "" || legacyAdmin != "" {
		if err := saveAt(configPath, secretPath, cfg); err != nil {
			return cfg, fmt.Errorf("migrate credentials to secret file store: %w", err)
		}
	}
	return cfg, nil
}

func migrateLegacyServerConfig(path string, data []byte, cfg *Config) error {
	var legacy struct {
		Server map[string]any `json:"server"`
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &legacy); err != nil {
		return err
	}
	if _, exists := legacy.Server["expose"]; exists {
		return nil
	}
	host, _ := legacy.Server["host"].(string)
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "" {
		return nil
	}
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		cfg.Server.Expose = ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}
	} else if host == "0.0.0.0" {
		cfg.Server.Expose = ExposureConfig{Mode: ExposureWildcard, Interfaces: []string{}}
	} else {
		cfg.Server.Expose = ExposureConfig{Mode: ExposureAll, Interfaces: []string{}}
	}
	return nil
}

func Save(cfg Config) error {
	path := DefaultPath()
	return saveAt(path, configformat.StructuredPathFrom(path, "tunnel"), cfg)
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
	persisted.Server.Expose = NormalizeExposure(persisted.Server.Expose)
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
	auth := ensureGenericObject(overlayRoot, "auth")
	auth["mcp_token_hash"] = persisted.Auth.MCPTokenHash
	auth["admin_token_hash"] = persisted.Auth.AdminTokenHash
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
