package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/secretstore"
	"go.mewis.me/codemcp/internal/tunnel"
)

type tunnelSecret struct {
	Version              int                `json:"version"`
	RuntimeKeyConfigured bool               `json:"runtime_key_configured,omitempty"`
	APIKey               string             `json:"api_key,omitempty"`
	Admin                *tunnelAdminSecret `json:"admin,omitempty"`
}

type tunnelAdminSecret struct {
	KeyConfigured  bool   `json:"key_configured,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	TenantID       string `json:"tenant_id,omitempty"`
	Verified       bool   `json:"verified,omitempty"`
	ReadAccess     bool   `json:"read_access,omitempty"`
	ManageAccess   bool   `json:"manage_access,omitempty"`
}

const tunnelSecretVersion = 1

var (
	tunnelRuntimeSecretName = secretstore.AccountName(secretstore.DomainTunnel, "runtime-key")
	tunnelAdminSecretName   = secretstore.AccountName(secretstore.DomainTunnel, "admin-key")
)

func TunnelSecretPath() string { return configformat.StructuredPath(RootPath(), "tunnel") }

func TunnelSecretEntries(root string) ([]string, error) {
	stored, err := loadTunnelSecretAt(configformat.StructuredPath(root, "tunnel"))
	if err != nil {
		return nil, err
	}
	entries := []string{}
	if stored.RuntimeKeyConfigured {
		entries = append(entries, tunnelRuntimeSecretName)
	}
	if stored.Admin != nil && stored.Admin.KeyConfigured {
		entries = append(entries, tunnelAdminSecretName)
	}
	return entries, nil
}

func loadTunnelSecretAt(path string) (tunnelSecret, error) {
	data, _, err := readConfigFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tunnelSecret{}, nil
		}
		return tunnelSecret{}, err
	}
	var secret tunnelSecret
	if err := configformat.Unmarshal(configformat.JSON, data, &secret); err != nil {
		return tunnelSecret{}, err
	}
	if secret.Version != 0 && secret.Version != tunnelSecretVersion {
		return tunnelSecret{}, fmt.Errorf("unsupported tunnel state version: %d", secret.Version)
	}
	secret.Version = tunnelSecretVersion
	return secret, nil
}

type tunnelSecretLoadPolicy struct {
	allowMissingRuntime bool
	allowMissingAdmin   bool
}

func loadTunnelSecretsWithPolicy(path string, cfg *tunnel.Config, legacyRuntime, legacyAdmin string, policy tunnelSecretLoadPolicy) (bool, error) {
	stored, err := loadTunnelSecretAt(path)
	if err != nil {
		return false, err
	}
	if stored.APIKey == secretFileMarker {
		stored.APIKey = ""
	}
	if stored.Admin != nil {
		if stored.Admin.Enabled != nil {
			tunnel.SetAdminEnabled(cfg, *stored.Admin.Enabled)
		}
		cfg.Admin.OrganizationID = stored.Admin.OrganizationID
		cfg.Admin.WorkspaceID = stored.Admin.WorkspaceID
		cfg.Admin.TenantID = stored.Admin.TenantID
		cfg.Admin.Verified = stored.Admin.Verified || stored.Admin.ReadAccess || stored.Admin.ManageAccess
		cfg.Admin.ReadAccess = stored.Admin.ReadAccess
		cfg.Admin.ManageAccess = stored.Admin.ManageAccess
	}
	if stored.APIKey != "" {
		legacyRuntime = stored.APIKey
	}
	store := secretstore.New(filepath.Dir(path))
	runtimeKey, runtimeMigration, err := resolveStoredSecret(store, tunnelRuntimeSecretName, stored.RuntimeKeyConfigured, legacyRuntime, "tunnel runtime key", policy.allowMissingRuntime)
	if err != nil {
		return false, err
	}
	adminConfigured := stored.Admin != nil && stored.Admin.KeyConfigured
	adminKey, adminMigration, err := resolveStoredSecret(store, tunnelAdminSecretName, adminConfigured, legacyAdmin, "tunnel admin key", policy.allowMissingAdmin)
	if err != nil {
		return false, err
	}
	cfg.APIKey = runtimeKey
	cfg.Admin.Key = adminKey
	return runtimeMigration || adminMigration || stored.APIKey != "", nil
}

func resolveStoredSecret(store *secretstore.Store, name string, configured bool, legacy, label string, allowMissing bool) (string, bool, error) {
	if configured {
		value, err := store.Get(name)
		if err == nil {
			return value, legacy != "", nil
		}
		if !errors.Is(err, secretstore.ErrNotFound) {
			return "", false, fmt.Errorf("load %s from secret file store: %w", label, err)
		}
		if legacy == "" {
			if allowMissing {
				return "", false, nil
			}
			return "", false, fmt.Errorf("%s is configured but missing from secret file store", label)
		}
	}
	if legacy != "" {
		return legacy, true, nil
	}
	return "", false, nil
}

func saveTunnelSecretAt(path string, cfg tunnel.Config) error {
	previous, err := loadTunnelSecretAt(path)
	if err != nil {
		return err
	}
	var adminEnabled *bool
	if !tunnel.AdminEnabled(cfg) || (previous.Admin != nil && previous.Admin.Enabled != nil) {
		adminEnabled = boolRef(tunnel.AdminEnabled(cfg))
	}
	admin := &tunnelAdminSecret{
		KeyConfigured:  cfg.Admin.Key != "",
		Enabled:        adminEnabled,
		OrganizationID: cfg.Admin.OrganizationID,
		WorkspaceID:    cfg.Admin.WorkspaceID,
		TenantID:       cfg.Admin.TenantID,
		Verified:       cfg.Admin.Verified || cfg.Admin.ReadAccess || cfg.Admin.ManageAccess,
		ReadAccess:     cfg.Admin.ReadAccess,
		ManageAccess:   cfg.Admin.ManageAccess,
	}
	if !admin.KeyConfigured && admin.Enabled == nil && admin.OrganizationID == "" && admin.WorkspaceID == "" && admin.TenantID == "" && !admin.Verified && !admin.ReadAccess && !admin.ManageAccess {
		admin = nil
	}
	stored := tunnelSecret{
		Version:              tunnelSecretVersion,
		RuntimeKeyConfigured: cfg.APIKey != "",
		Admin:                admin,
	}
	exists := true
	if _, _, err := readConfigFile(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		exists = false
	}
	if exists || stored.RuntimeKeyConfigured || stored.Admin != nil {
		data, err := mergeTunnelSecretData(path, stored, cfg)
		if err != nil {
			return err
		}
		if err := writeConfigFile(path, data, 0600); err != nil {
			return err
		}
	}
	changes := make([]secretstore.Change, 0, 2)
	if stored.RuntimeKeyConfigured || previous.RuntimeKeyConfigured || previous.APIKey != "" {
		changes = append(changes, secretstore.Change{Name: tunnelRuntimeSecretName, Value: cfg.APIKey})
	}
	storedAdminConfigured := stored.Admin != nil && stored.Admin.KeyConfigured
	previousAdminConfigured := previous.Admin != nil && previous.Admin.KeyConfigured
	if storedAdminConfigured || previousAdminConfigured {
		changes = append(changes, secretstore.Change{Name: tunnelAdminSecretName, Value: cfg.Admin.Key})
	}
	return secretstore.New(filepath.Dir(path)).Apply(changes)
}

func mergeTunnelSecretData(path string, stored tunnelSecret, runtime tunnel.Config) ([]byte, error) {
	stored.Version = tunnelSecretVersion
	overlayData, err := configformat.Marshal(configformat.JSON, stored)
	if err != nil {
		return nil, err
	}
	overlay, err := configformat.DecodeGeneric(configformat.JSON, overlayData)
	if err != nil {
		return nil, err
	}
	overlayRoot, ok := overlay.(map[string]any)
	if !ok {
		return nil, errors.New("tunnel configuration must encode as an object")
	}
	overlayRoot["runtime_key_configured"] = stored.RuntimeKeyConfigured
	overlayRoot["version"] = int64(tunnelSecretVersion)
	if stored.Admin != nil {
		admin := map[string]any{
			"key_configured":  stored.Admin.KeyConfigured,
			"organization_id": stored.Admin.OrganizationID,
			"workspace_id":    stored.Admin.WorkspaceID,
			"tenant_id":       stored.Admin.TenantID,
			"verified":        stored.Admin.Verified,
			"read_access":     stored.Admin.ReadAccess,
			"manage_access":   stored.Admin.ManageAccess,
		}
		if stored.Admin.Enabled != nil {
			admin["enabled"] = *stored.Admin.Enabled
		}
		overlayRoot["admin"] = admin
	}

	var base any = map[string]any{}
	existingData, _, readErr := readConfigFile(path)
	if readErr == nil {
		base, err = configformat.DecodeGeneric(configformat.JSON, existingData)
		if err != nil {
			return nil, fmt.Errorf("decode existing tunnel configuration for merge: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	merged, ok := configformat.MergeGeneric(base, overlayRoot).(map[string]any)
	if !ok {
		return nil, errors.New("tunnel configuration must be an object")
	}
	if existing, ok := base.(map[string]any); ok {
		if _, exists := existing["api_key"]; exists {
			merged["api_key"] = secretMarkerValue(runtime.APIKey)
		}
	}
	if stored.Admin == nil {
		delete(merged, "admin")
	}
	return configformat.EncodeGeneric(configformat.JSON, merged)
}

func boolRef(value bool) *bool { return &value }
