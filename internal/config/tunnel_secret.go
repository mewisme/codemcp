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
	Version              int    `json:"version"`
	RuntimeKeyConfigured bool   `json:"runtime_key_configured,omitempty"`
	AdminKeyConfigured   bool   `json:"admin_key_configured,omitempty"`
	APIKey               string `json:"api_key,omitempty"`
	AdminKey             string `json:"admin_key,omitempty"`
	AdminOrganizationID  string `json:"admin_organization_id,omitempty"`
	AdminWorkspaceID     string `json:"admin_workspace_id,omitempty"`
	AdminTenantID        string `json:"admin_tenant_id,omitempty"`
	AdminReadAccess      bool   `json:"admin_read_access,omitempty"`
	AdminManageAccess    bool   `json:"admin_manage_access,omitempty"`
}

const tunnelSecretVersion = 1

var (
	tunnelRuntimeSecretName = secretstore.Name("tunnel", "runtime-key")
	tunnelAdminSecretName   = secretstore.Name("tunnel", "admin-key")
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
	if stored.AdminKeyConfigured {
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
	if stored.AdminKey == secretFileMarker {
		stored.AdminKey = ""
	}
	if stored.AdminOrganizationID != "" || stored.AdminWorkspaceID != "" || stored.AdminTenantID != "" || stored.AdminReadAccess || stored.AdminManageAccess {
		cfg.AdminOrganizationID = stored.AdminOrganizationID
		cfg.AdminWorkspaceID = stored.AdminWorkspaceID
		cfg.AdminTenantID = stored.AdminTenantID
	}
	cfg.AdminReadAccess = stored.AdminReadAccess
	cfg.AdminManageAccess = stored.AdminManageAccess
	if stored.APIKey != "" {
		legacyRuntime = stored.APIKey
	}
	if stored.AdminKey != "" {
		legacyAdmin = stored.AdminKey
	}
	store := secretstore.New(filepath.Dir(path))
	runtimeKey, runtimeMigration, err := resolveStoredSecret(store, tunnelRuntimeSecretName, stored.RuntimeKeyConfigured, legacyRuntime, "tunnel runtime key", policy.allowMissingRuntime)
	if err != nil {
		return false, err
	}
	adminKey, adminMigration, err := resolveStoredSecret(store, tunnelAdminSecretName, stored.AdminKeyConfigured, legacyAdmin, "tunnel admin key", policy.allowMissingAdmin)
	if err != nil {
		return false, err
	}
	cfg.APIKey = runtimeKey
	cfg.AdminKey = adminKey
	return runtimeMigration || adminMigration || stored.APIKey != "" || stored.AdminKey != "", nil
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
	stored := tunnelSecret{
		Version:              tunnelSecretVersion,
		RuntimeKeyConfigured: cfg.APIKey != "", AdminKeyConfigured: cfg.AdminKey != "",
		AdminOrganizationID: cfg.AdminOrganizationID, AdminWorkspaceID: cfg.AdminWorkspaceID, AdminTenantID: cfg.AdminTenantID,
		AdminReadAccess: cfg.AdminReadAccess, AdminManageAccess: cfg.AdminManageAccess,
	}
	exists := true
	if _, _, err := readConfigFile(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		exists = false
	}
	if exists || stored.RuntimeKeyConfigured || stored.AdminKeyConfigured || stored.AdminOrganizationID != "" || stored.AdminWorkspaceID != "" || stored.AdminTenantID != "" || stored.AdminReadAccess || stored.AdminManageAccess {
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
	if stored.AdminKeyConfigured || previous.AdminKeyConfigured || previous.AdminKey != "" {
		changes = append(changes, secretstore.Change{Name: tunnelAdminSecretName, Value: cfg.AdminKey})
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
	overlayRoot["admin_key_configured"] = stored.AdminKeyConfigured
	overlayRoot["admin_organization_id"] = stored.AdminOrganizationID
	overlayRoot["admin_workspace_id"] = stored.AdminWorkspaceID
	overlayRoot["admin_tenant_id"] = stored.AdminTenantID
	overlayRoot["admin_read_access"] = stored.AdminReadAccess
	overlayRoot["admin_manage_access"] = stored.AdminManageAccess

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
		if _, exists := existing["admin_key"]; exists {
			merged["admin_key"] = secretMarkerValue(runtime.AdminKey)
		}
	}
	return configformat.EncodeGeneric(configformat.JSON, merged)
}
