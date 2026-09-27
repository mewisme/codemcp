package config

import (
	"os"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tunnel"
)

type Inspection struct {
	Exists                     bool
	Config                     Config
	TunnelRuntimeKeyConfigured bool
	TunnelAdminKeyConfigured   bool
}

func Inspect() (Inspection, error) {
	source, err := Source()
	if err != nil {
		return Inspection{}, err
	}
	result := Inspection{Exists: source.Exists, Config: Default()}
	if !source.Exists {
		return result, nil
	}
	data, _, err := readConfigFile(source.Path)
	if err != nil {
		return Inspection{}, err
	}
	if err := configformat.Unmarshal(configformat.JSON, data, &result.Config); err != nil {
		return Inspection{}, err
	}
	result.Config.Server.Expose = NormalizeExposure(result.Config.Server.Expose)
	if err := migrateLegacyServerConfig(source.Path, data, &result.Config); err != nil {
		return Inspection{}, err
	}

	runtimeLegacy := result.Config.Tunnel.APIKey != "" && result.Config.Tunnel.APIKey != secretFileMarker
	adminLegacy := result.Config.Tunnel.Admin.Key != "" && result.Config.Tunnel.Admin.Key != secretFileMarker
	stored, err := loadTunnelSecretAt(configformat.StructuredPathFrom(source.Path, "tunnel"))
	if err != nil && !os.IsNotExist(err) {
		return Inspection{}, err
	}
	result.TunnelRuntimeKeyConfigured = stored.RuntimeKeyConfigured || stored.APIKey != "" || runtimeLegacy
	result.TunnelAdminKeyConfigured = adminLegacy || stored.Admin != nil && stored.Admin.KeyConfigured
	if stored.Admin != nil {
		if stored.Admin.Enabled != nil {
			tunnel.SetAdminEnabled(&result.Config.Tunnel, *stored.Admin.Enabled)
		}
		result.Config.Tunnel.Admin.OrganizationID = stored.Admin.OrganizationID
		result.Config.Tunnel.Admin.WorkspaceID = stored.Admin.WorkspaceID
		result.Config.Tunnel.Admin.TenantID = stored.Admin.TenantID
		result.Config.Tunnel.Admin.Verified = stored.Admin.Verified || stored.Admin.ReadAccess || stored.Admin.ManageAccess
		result.Config.Tunnel.Admin.ReadAccess = stored.Admin.ReadAccess
		result.Config.Tunnel.Admin.ManageAccess = stored.Admin.ManageAccess
	}
	result.Config.Tunnel.APIKey = ""
	result.Config.Tunnel.Admin.Key = ""
	return result, nil
}
