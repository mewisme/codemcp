package config

import (
	"net/url"
	"strings"
)

type SettingPresentationPolicy string

const (
	SettingPresentationValue           SettingPresentationPolicy = "value"
	SettingPresentationConfiguredState SettingPresentationPolicy = "configured-state"
	SettingPresentationMaskedPreview   SettingPresentationPolicy = "masked-preview"
	SettingPresentationInternal        SettingPresentationPolicy = "internal"
)

type FieldSelectorSpec struct {
	Template        string
	Resource        string
	InventoryOwner  string
	CompletionOwner string
	IDValidator     string
	IDEncoding      string
}

type FieldSelectorMatch struct {
	Spec       FieldSpec
	ResourceID string
}

var virtualSettingSpecs = []FieldSpec{
	{
		Key: "auth.mcp_token", Label: "MCP token", Section: FieldSectionAccess, Kind: FieldString,
		Virtual: true, Secret: true, Rotatable: true, ConfiguredStateKey: "auth.mcp_token_configured",
		Presentation: SettingPresentationConfiguredState, ApplicationOwner: "auth.credentials",
		ScopedCommands: []string{"auth mcp create", "auth status"},
	},
	{
		Key: "auth.admin_token", Label: "Admin token", Section: FieldSectionAccess, Kind: FieldString,
		Virtual: true, Secret: true, Rotatable: true, ConfiguredStateKey: "auth.admin_token_configured",
		Presentation: SettingPresentationConfiguredState, ApplicationOwner: "auth.credentials",
		ScopedCommands: []string{"auth admin create", "auth status"},
	},
	{
		Key: "auth.mcp_token_configured", Label: "MCP token configured", Section: FieldSectionAccess, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "auth.credentials",
		ScopedCommands: []string{"auth status"},
	},
	{
		Key: "auth.admin_token_configured", Label: "Admin token configured", Section: FieldSectionAccess, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "auth.credentials",
		ScopedCommands: []string{"auth status"},
	},
	{
		Key: "tunnel.api_key_configured", Label: "Tunnel runtime key configured", Section: FieldSectionTunnel, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "tunnel.credentials",
		ScopedCommands: []string{"tunnel status"},
	},
	{
		Key: "tunnel.admin_key_configured", Label: "Tunnel admin key configured", Section: FieldSectionTunnel, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "tunnel.credentials",
		ScopedCommands: []string{"tunnel admin key status"},
	},

	{Key: "upstream.servers[<id>].enabled", Label: "Upstream enabled", Section: FieldSectionIntegrations, Kind: FieldBool, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].enabled")},
	{Key: "upstream.servers[<id>].name", Label: "Upstream name", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].name")},
	{Key: "upstream.servers[<id>].transport", Label: "Upstream transport", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"stdio", "http"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].transport")},
	{Key: "upstream.servers[<id>].command", Label: "Upstream command", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].command")},
	{Key: "upstream.servers[<id>].args", Label: "Upstream arguments", Section: FieldSectionIntegrations, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].args")},
	{Key: "upstream.servers[<id>].cwd", Label: "Upstream working directory", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].cwd")},
	{Key: "upstream.servers[<id>].url", Label: "Upstream URL", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].url")},
	{Key: "upstream.servers[<id>].bearer_token_env_var", Label: "Upstream bearer-token environment variable", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].bearer_token_env_var")},
	{Key: "upstream.servers[<id>].auth.type", Label: "Upstream auth mode", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"auto", "oauth", "none"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].auth.type")},
	{Key: "upstream.servers[<id>].auth.scope", Label: "Upstream OAuth scope", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].auth.scope")},
	{Key: "upstream.servers[<id>].tool_prefix", Label: "Upstream tool prefix", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].tool_prefix")},
	{Key: "upstream.servers[<id>].expose", Label: "Upstream tool exposure", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"none", "meta_only", "allowlist", "all"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].expose")},
	{Key: "upstream.servers[<id>].tools", Label: "Upstream allowed tools", Section: FieldSectionIntegrations, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].tools")},
	{Key: "upstream.servers[<id>].disabled_tools", Label: "Upstream disabled tools", Section: FieldSectionIntegrations, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].disabled_tools")},
	{Key: "upstream.servers[<id>].idle_timeout_sec", Label: "Upstream idle timeout", Section: FieldSectionIntegrations, Kind: FieldInt, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].idle_timeout_sec")},
	{Key: "upstream.servers[<id>].allow_private_network", Label: "Upstream private-network access", Section: FieldSectionIntegrations, Kind: FieldBool, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure"}, Selector: upstreamServerSelector("upstream.servers[<id>].allow_private_network")},

	{Key: "tunnel.managed[<id>].name", Label: "Managed tunnel name", Section: FieldSectionTunnel, Kind: FieldString, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "tunnel.managed", ScopedCommands: []string{"tunnel update"}, Selector: managedTunnelSelector("tunnel.managed[<id>].name")},
	{Key: "tunnel.managed[<id>].description", Label: "Managed tunnel description", Section: FieldSectionTunnel, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "tunnel.managed", ScopedCommands: []string{"tunnel update"}, Selector: managedTunnelSelector("tunnel.managed[<id>].description")},
	{Key: "tunnel.managed[<id>].tenant_ids", Label: "Managed tunnel tenants", Section: FieldSectionTunnel, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "tunnel.managed", ScopedCommands: []string{"tunnel update"}, Selector: managedTunnelSelector("tunnel.managed[<id>].tenant_ids")},
	{Key: "tunnel.managed[<id>].workspace_ids", Label: "Managed tunnel workspaces", Section: FieldSectionTunnel, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "tunnel.managed", ScopedCommands: []string{"tunnel update"}, Selector: managedTunnelSelector("tunnel.managed[<id>].workspace_ids")},
	{Key: "tunnel.managed[<id>].organization_ids", Label: "Managed tunnel organizations", Section: FieldSectionTunnel, Kind: FieldList, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "tunnel.managed", ScopedCommands: []string{"tunnel update"}, Selector: managedTunnelSelector("tunnel.managed[<id>].organization_ids")},
}

func Settings() []FieldSpec {
	result := make([]FieldSpec, 0, len(fieldSpecs)+len(virtualSettingSpecs))
	for _, spec := range fieldSpecs {
		result = append(result, cloneFieldSpec(normalizeSettingSpec(spec)))
	}
	for _, spec := range virtualSettingSpecs {
		result = append(result, cloneFieldSpec(normalizeSettingSpec(spec)))
	}
	return result
}

func SettingByKey(key string) (FieldSpec, bool) {
	key = canonicalFieldKey(key)
	for _, spec := range Settings() {
		if spec.Key == key {
			return spec, true
		}
	}
	return FieldSpec{}, false
}

func SettingsByPrefix(prefix string) []FieldSpec {
	prefix = strings.TrimSpace(prefix)
	result := make([]FieldSpec, 0)
	for _, spec := range Settings() {
		if spec.InternalOnly || !spec.Readable {
			continue
		}
		if prefix == "" || spec.Key == prefix || strings.HasPrefix(spec.Key, prefix+".") || strings.HasPrefix(spec.Key, prefix+"[") {
			result = append(result, spec)
		}
	}
	return result
}

func MatchSettingSelector(key string) (FieldSelectorMatch, bool) {
	key = strings.TrimSpace(key)
	for _, raw := range virtualSettingSpecs {
		if raw.Selector == nil {
			continue
		}
		spec := normalizeSettingSpec(raw)
		before, after, ok := strings.Cut(spec.Selector.Template, "<id>")
		if !ok || !strings.HasPrefix(key, before) || !strings.HasSuffix(key, after) {
			continue
		}
		encoded := strings.TrimSuffix(strings.TrimPrefix(key, before), after)
		if encoded == "" || strings.ContainsAny(encoded, "[]/") {
			continue
		}
		id, err := url.PathUnescape(encoded)
		if err != nil || id == "" || strings.ContainsAny(id, "[]/") {
			continue
		}
		return FieldSelectorMatch{Spec: cloneFieldSpec(spec), ResourceID: id}, true
	}
	return FieldSelectorMatch{}, false
}

func normalizeSettingSpec(spec FieldSpec) FieldSpec {
	if spec.Domain == "" {
		spec.Domain = settingDomain(spec.Key)
	}
	if spec.ApplicationOwner == "" {
		spec.ApplicationOwner = "config"
	}
	if spec.Presentation == "" {
		spec.Presentation = SettingPresentationValue
	}
	if !spec.Virtual {
		spec.Readable = !spec.Sensitive
		spec.Writable = spec.Editable
		spec.DefaultReset = spec.Editable
	}
	if spec.Readable && spec.ReadKey == "" {
		spec.ReadKey = spec.Key
	}
	if spec.Writable && spec.WriteKey == "" {
		spec.WriteKey = spec.Key
	}

	switch spec.Key {
	case "auth.mcp_enabled":
		spec.ApplicationOwner = "auth"
		spec.ScopedCommands = []string{"auth mcp enable", "auth mcp disable"}
	case "auth.admin_enabled":
		spec.ApplicationOwner = "auth"
		spec.ScopedCommands = []string{"auth admin enable", "auth admin disable"}
	case "auth.mcp_legacy_bearer":
		spec.ApplicationOwner = "auth"
		spec.ScopedExemption = "no dedicated scoped CLI facade on current main"
	case "auth.mcp_token_hash", "auth.admin_token_hash":
		spec.InternalOnly = true
		spec.Readable, spec.Writable, spec.DefaultReset = false, false, false
		spec.ReadKey, spec.WriteKey = "", ""
		spec.Presentation = SettingPresentationInternal
		spec.ApplicationOwner = "auth.credentials"
	case "tunnel.enabled":
		spec.ApplicationOwner = "tunnel.runtime"
		spec.ScopedCommands = []string{"tunnel configure", "tunnel enable", "tunnel disable"}
	case "tunnel.id", "tunnel.control_plane_base_url", "tunnel.organization_id":
		spec.ApplicationOwner = "tunnel.runtime"
		spec.ScopedCommands = []string{"tunnel configure"}
	case "tunnel.api_key":
		spec.Kind = FieldString
		spec.Readable, spec.Writable, spec.Secret, spec.Clearable, spec.DefaultReset = false, true, true, true, false
		spec.ReadKey, spec.WriteKey = "", spec.Key
		spec.ConfiguredStateKey = "tunnel.api_key_configured"
		spec.Presentation = SettingPresentationMaskedPreview
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel configure", "tunnel use"}
	case "tunnel.admin_key":
		spec.Kind = FieldString
		spec.Readable, spec.Writable, spec.Secret, spec.Clearable, spec.Verifiable, spec.DefaultReset = false, true, true, true, true, false
		spec.ReadKey, spec.WriteKey = "", spec.Key
		spec.ConfiguredStateKey = "tunnel.admin_key_configured"
		spec.Presentation = SettingPresentationMaskedPreview
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel admin key set", "tunnel admin key remove", "tunnel admin key verify", "tunnel admin key status"}
	case "tunnel.admin_organization_id", "tunnel.admin_workspace_id", "tunnel.admin_tenant_id":
		spec.Derived = true
		spec.Readable, spec.Writable, spec.DefaultReset = true, false, false
		spec.ReadKey = spec.Key
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel admin key status", "tunnel admin key verify"}
	}

	if len(spec.ScopedCommands) == 0 && spec.ScopedExemption == "" && !spec.InternalOnly {
		spec.ScopedExemption = scopedSettingExemption(spec.Domain)
	}
	return spec
}

func settingDomain(key string) string {
	if index := strings.IndexAny(key, ".["); index > 0 {
		return key[:index]
	}
	return key
}

func scopedSettingExemption(domain string) string {
	switch domain {
	case "server", "admin", "permissions", "shell":
		return "no dedicated scoped CLI facade on current main"
	case "integrations":
		return "integration is persisted through the config surface on current main"
	default:
		return "config-only setting on current main"
	}
}

func upstreamServerSelector(template string) *FieldSelectorSpec {
	return &FieldSelectorSpec{
		Template: template, Resource: "upstream.server", InventoryOwner: "upstream server manager",
		CompletionOwner: "upstream server inventory", IDValidator: "upstream server identity",
		IDEncoding: "bracket segment with RFC3986 percent-decoding",
	}
}

func managedTunnelSelector(template string) *FieldSelectorSpec {
	return &FieldSelectorSpec{
		Template: template, Resource: "tunnel.managed", InventoryOwner: "OpenAI managed tunnel inventory",
		CompletionOwner: "managed tunnel inventory", IDValidator: "non-empty OpenAI tunnel identity",
		IDEncoding: "bracket segment with RFC3986 percent-decoding",
	}
}
