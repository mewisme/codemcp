package config

import (
	"net/url"
	"strings"
)

type SettingPresentationPolicy string

const (
	SettingPresentationValue         SettingPresentationPolicy = "value"
	SettingPresentationMaskedPreview SettingPresentationPolicy = "masked-preview"
	SettingPresentationInternal      SettingPresentationPolicy = "internal"
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
		Key: "telegram.token", Label: "Telegram bot token", Section: FieldSectionAccess, Kind: FieldString,
		Description: "stores the managed Telegram bot credential",
		Details:     "The raw bot token is stored only in the canonical secret store. Saving or removing it does not contact Telegram; runtime activation validates the token before polling.",
		Guidance:    "Manage this credential with cm telegram token set/remove or the equivalent config set/unset commands.",
		Virtual:     true, Writable: true, Secret: true, Clearable: true, ConfiguredStateKey: "telegram.token_configured",
		Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "telegram.credentials",
		ValueRole:      SettingValueConfigured,
		ScopedCommands: []string{"telegram token set", "telegram token remove", "telegram token status"},
	},
	{
		Key: "telegram.token_configured", Label: "Telegram bot token configured", Section: FieldSectionAccess, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "telegram.credentials",
		ScopedCommands: []string{"telegram token status"},
	},
	{
		Key: "integrations.typesafe.api_key", Label: "TypeSafe API key", Section: FieldSectionIntegrations, Kind: FieldString,
		Description: "stores the managed TypeSafe API credential",
		Details:     "The raw API key is stored only in the canonical secret store. Saving or removing it never probes TypeSafe.",
		Guidance:    "Manage this credential with the TypeSafe integration key commands.",
		Virtual:     true, Writable: true, Secret: true, Clearable: true, ConfiguredStateKey: "integrations.typesafe.api_key_configured",
		Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "integration:typesafe",
		ValueRole:      SettingValueConfigured,
		ScopedCommands: []string{"integration typesafe key set", "integration typesafe key remove"},
	},
	{
		Key: "integrations.typesafe.api_key_configured", Label: "TypeSafe API key configured", Section: FieldSectionIntegrations, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "integration:typesafe",
		ScopedCommands: []string{"integration typesafe status"},
	},
	{
		Key: "llm.provider", Label: "Active LLM provider", Section: FieldSectionIntegrations, Kind: FieldString,
		Description: "selects the active LLM provider",
		Virtual:     true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ValueRole: SettingValueConfigured,
		ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner",
	},
	{
		Key: "llm.api_key", Label: "Active LLM API key", Section: FieldSectionIntegrations, Kind: FieldString,
		Description: "stores the managed credential for the active LLM provider",
		Details:     "The raw API key is stored only in the canonical secret store and this key aliases the currently selected provider.",
		Virtual:     true, Writable: true, Secret: true, Clearable: true, ConfiguredStateKey: "llm.api_key_configured",
		Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "llm.providers", ValueRole: SettingValueConfigured,
		ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner",
	},
	{
		Key: "llm.api_key_configured", Label: "Active LLM API key configured", Section: FieldSectionIntegrations, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "llm.providers", ValueRole: SettingValueDerived,
		ScopedExemption: "derived LLM credential state is exposed through the LLM application owner",
	},
	{
		Key: "llm.base_url", Label: "Active LLM base URL", Section: FieldSectionIntegrations, Kind: FieldString,
		Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ValueRole: SettingValueConfigured,
		ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner",
	},
	{
		Key: "llm.model", Label: "Active LLM model", Section: FieldSectionIntegrations, Kind: FieldString,
		Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "llm.providers", ValueRole: SettingValueConfigured,
		ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner",
	},
	{
		Key: "auth.mcp_token", Label: "MCP token", Section: FieldSectionAccess, Kind: FieldString,
		Virtual: true, Secret: true, Rotatable: true, ConfiguredStateKey: "auth.mcp_token_configured",
		Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "auth.credentials",
		ValueRole:      SettingValueGenerated,
		ScopedCommands: []string{"auth mcp create", "auth status"},
	},
	{
		Key: "auth.admin_token", Label: "Admin token", Section: FieldSectionAccess, Kind: FieldString,
		Virtual: true, Secret: true, Rotatable: true, ConfiguredStateKey: "auth.admin_token_configured",
		Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "auth.credentials",
		ValueRole:      SettingValueGenerated,
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
		Key: "tunnel.admin.key_configured", Label: "Tunnel admin key configured", Section: FieldSectionTunnel, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "tunnel.credentials",
		ScopedCommands: []string{"tunnel admin key status"},
	},
	{
		Key: "tunnel.admin.configured", Label: "Tunnel admin configured", Section: FieldSectionTunnel, Kind: FieldBool,
		Virtual: true, Derived: true, Readable: true, ApplicationOwner: "tunnel.credentials",
		ScopedCommands: []string{"tunnel admin key status"},
	},

	{Key: "upstream.servers[<id>].enabled", Label: "Upstream enabled", Section: FieldSectionIntegrations, Kind: FieldBool, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "upstream.servers", ScopedCommands: []string{"upstream server configure", "upstream server enable", "upstream server disable"}, Selector: upstreamServerSelector("upstream.servers[<id>].enabled")},
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

	{Key: "llm.providers[<id>].name", Label: "LLM provider name", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].name")},
	{Key: "llm.providers[<id>].protocol", Label: "LLM provider protocol", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"openai", "anthropic"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].protocol")},
	{Key: "llm.providers[<id>].base_url", Label: "LLM provider base URL", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].base_url")},
	{Key: "llm.providers[<id>].model", Label: "LLM provider model", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Readable: true, Writable: true, Clearable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].model")},
	{Key: "llm.providers[<id>].api_key", Label: "LLM provider API key", Section: FieldSectionIntegrations, Kind: FieldString, Virtual: true, Writable: true, Secret: true, Clearable: true, ConfiguredStateKey: "llm.providers[<id>].api_key_configured", Presentation: SettingPresentationMaskedPreview, ApplicationOwner: "llm.providers", ValueRole: SettingValueConfigured, ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].api_key")},
	{Key: "llm.providers[<id>].api_key_configured", Label: "LLM provider API key configured", Section: FieldSectionIntegrations, Kind: FieldBool, Virtual: true, Derived: true, Readable: true, ApplicationOwner: "llm.providers", ValueRole: SettingValueDerived, ScopedExemption: "derived LLM credential state is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].api_key_configured")},
	{Key: "llm.providers[<id>].auth_mode", Label: "LLM provider auth mode", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"none", "bearer", "x-api-key"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].auth_mode")},
	{Key: "llm.providers[<id>].discovery", Label: "LLM provider discovery", Section: FieldSectionIntegrations, Kind: FieldEnum, Options: []string{"none", "openai-models", "ollama-tags"}, Virtual: true, Readable: true, Writable: true, ApplicationOwner: "llm.providers", ScopedExemption: "dedicated LLM provider administration is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].discovery")},
	{Key: "llm.providers[<id>].core", Label: "LLM core provider", Section: FieldSectionIntegrations, Kind: FieldBool, Virtual: true, Derived: true, Readable: true, ApplicationOwner: "llm.providers", ScopedExemption: "derived LLM provider identity is exposed through the LLM application owner", Selector: llmProviderSelector("llm.providers[<id>].core")},

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
		if encoded == "" || encoded == "<id>" || strings.ContainsAny(encoded, "[]/") {
			continue
		}
		id, err := url.PathUnescape(encoded)
		if err != nil || id == "" || id == "<id>" || strings.ContainsAny(id, "[]/") {
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
	case "server.enabled":
		spec.ScopedCommands = []string{"server enable", "server disable"}
	case "server.expose.mode":
		spec.ScopedCommands = []string{"server expose mode"}
	case "server.expose.interfaces":
		spec.ScopedCommands = []string{"server interface add", "server interface remove"}
	case "server.port":
		spec.ScopedCommands = []string{"server port"}
	case "server.allow_insecure_http":
		spec.ScopedCommands = []string{"server insecure http allow", "server insecure http deny"}
	case "server.allow_unauthenticated_loopback":
		spec.ScopedCommands = []string{"server loopback auth allow", "server loopback auth require"}
	case "admin.enabled":
		spec.ScopedCommands = []string{"admin enable", "admin disable"}
	case "admin.port":
		spec.ScopedCommands = []string{"admin port"}
	case "auth.mcp_enabled":
		spec.ApplicationOwner = "auth"
		spec.ScopedCommands = []string{"auth mcp enable", "auth mcp disable"}
	case "auth.admin_enabled":
		spec.ApplicationOwner = "auth"
		spec.ScopedCommands = []string{"auth admin enable", "auth admin disable"}
	case "auth.mcp_legacy_bearer":
		spec.ApplicationOwner = "auth"
		spec.ScopedCommands = []string{"auth mcp legacy bearer enable", "auth mcp legacy bearer disable"}
	case "auth.mcp_token_hash", "auth.admin_token_hash":
		spec.InternalOnly = true
		spec.Readable, spec.Writable, spec.DefaultReset = false, false, false
		spec.ReadKey, spec.WriteKey = "", ""
		spec.Presentation = SettingPresentationInternal
		spec.ApplicationOwner = "auth.credentials"
	case "permissions.allow_dirs":
		spec.ScopedCommands = []string{"permissions allow dir add", "permissions allow dir remove"}
	case "permissions.mcp_config_read", "permissions.mcp_config_write":
		spec.ApplicationOwner = "permissions.mcp_config"
		spec.ScopedExemption = "generic config set is the operator facade for MCP config eligibility"
	case "shell.path":
		spec.ScopedCommands = []string{"shell path"}
	case "telemetry.enabled":
		spec.ApplicationOwner = "telemetry"
		spec.ScopedCommands = []string{"telemetry enable", "telemetry disable"}
	case "telegram.enabled", "telegram.allowed_user_ids", "telegram.topics_enabled", "telegram.logs_mini_app.enabled":
		spec.ApplicationOwner = "telegram.runtime"
		spec.ScopedExemption = "Telegram setup and administration facades are introduced by the Telegram interface"
	case "approval.semantic.enabled", "approval.semantic.provider", "approval.semantic.timeout_ms", "approval.semantic.minimum_confidence", "approval.semantic.fail_mode", "approval.semantic.low_action", "approval.semantic.medium_action", "approval.semantic.high_action", "approval.semantic.critical_action":
		spec.ApplicationOwner = "approval.semantic"
		spec.ScopedExemption = "generic config set is the canonical operator facade for semantic approval policy"
	case "notifications.approval.enabled":
		spec.ApplicationOwner = "notifications.approval"
		spec.ScopedCommands = []string{"notification approval enable", "notification approval disable"}
	case "notifications.approval.pending":
		spec.ApplicationOwner = "notifications.approval"
		spec.ScopedCommands = []string{"notification approval pending enable", "notification approval pending disable"}
	case "notifications.approval.resolved":
		spec.ApplicationOwner = "notifications.approval"
		spec.ScopedCommands = []string{"notification approval resolved enable", "notification approval resolved disable"}
	case "notifications.approval.desktop_enabled":
		spec.ApplicationOwner = "notifications.approval"
		spec.ScopedCommands = []string{"notification desktop enable", "notification desktop disable"}
	case "notifications.approval.telegram_enabled":
		spec.ApplicationOwner = "notifications.approval"
		spec.ScopedCommands = []string{"notification telegram enable", "notification telegram disable"}
	case "notifications.completion.enabled":
		spec.ApplicationOwner = "notifications.completion"
		spec.ScopedCommands = []string{"notification completion enable", "notification completion disable"}
	case "notifications.completion.desktop_enabled":
		spec.ApplicationOwner = "notifications.completion"
		spec.ScopedCommands = []string{"notification completion desktop enable", "notification completion desktop disable"}
	case "notifications.completion.telegram_enabled":
		spec.ApplicationOwner = "notifications.completion"
		spec.ScopedCommands = []string{"notification completion telegram enable", "notification completion telegram disable"}
	case "integrations.ponytail.active":
		spec.ScopedCommands = []string{"integration ponytail enable", "integration ponytail disable"}
	case "integrations.ponytail.mode":
		spec.ScopedCommands = []string{"integration ponytail mode"}
	case "integrations.caveman.active":
		spec.ScopedCommands = []string{"integration caveman enable", "integration caveman disable"}
	case "integrations.caveman.mode":
		spec.ScopedCommands = []string{"integration caveman mode"}
	case "integrations.rtk.enabled":
		spec.ScopedCommands = []string{"integration rtk enable", "integration rtk disable"}
	case "integrations.rtk.path":
		spec.ScopedCommands = []string{"integration rtk path"}
	case "integrations.codegraph.enabled":
		spec.ScopedCommands = []string{"integration codegraph enable", "integration codegraph disable"}
	case "integrations.codegraph.path":
		spec.ScopedCommands = []string{"integration codegraph path"}
	case "integrations.typesafe.enabled":
		spec.ApplicationOwner = "integration:typesafe"
		spec.ScopedCommands = []string{"integration typesafe enable", "integration typesafe disable"}
	case "integrations.typesafe.model":
		spec.ApplicationOwner = "integration:typesafe"
		spec.ScopedCommands = []string{"integration typesafe model"}
	case "integrations.typesafe.timeout_ms":
		spec.ApplicationOwner = "integration:typesafe"
		spec.ScopedCommands = []string{"integration typesafe timeout"}
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
		spec.ScopedCommands = []string{"tunnel key set", "tunnel key remove", "tunnel configure", "tunnel use"}
	case "tunnel.admin.key":
		spec.Kind = FieldString
		spec.Readable, spec.Writable, spec.Secret, spec.Clearable, spec.Verifiable, spec.DefaultReset = false, true, true, true, true, false
		spec.ReadKey, spec.WriteKey = "", spec.Key
		spec.ConfiguredStateKey = "tunnel.admin.key_configured"
		spec.Presentation = SettingPresentationMaskedPreview
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel admin key set", "tunnel admin key remove", "tunnel admin verify", "tunnel admin key verify", "tunnel admin key status"}
	case "tunnel.admin.enabled":
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel admin enable", "tunnel admin disable"}
	case "tunnel.admin.organization_id", "tunnel.admin.workspace_id", "tunnel.admin.tenant_id":
		spec.Derived = false
		spec.Kind = FieldString
		spec.Readable, spec.Writable, spec.DefaultReset = true, true, false
		spec.ReadKey = spec.Key
		spec.WriteKey = spec.Key
		spec.ApplicationOwner = "tunnel.credentials"
		scopeName := strings.TrimSuffix(strings.TrimPrefix(spec.Key, "tunnel.admin."), "_id")
		spec.ScopedCommands = []string{"tunnel admin " + scopeName + " set", "tunnel admin key set", "tunnel admin key status", "tunnel admin verify", "tunnel admin key verify"}
	case "tunnel.admin.verified", "tunnel.admin.read_access", "tunnel.admin.manage_access":
		spec.Derived = true
		spec.Readable, spec.Writable, spec.DefaultReset = true, false, false
		spec.ReadKey, spec.WriteKey = spec.Key, ""
		spec.ApplicationOwner = "tunnel.credentials"
		spec.ScopedCommands = []string{"tunnel admin key status", "tunnel admin verify", "tunnel admin key verify"}
	}

	if spec.ValueRole == "" {
		switch {
		case spec.InternalOnly:
			spec.ValueRole = SettingValueInternal
		case spec.Derived:
			spec.ValueRole = SettingValueDerived
		default:
			spec.ValueRole = SettingValueConfigured
		}
	}

	return spec
}

func settingDomain(key string) string {
	if index := strings.IndexAny(key, ".["); index > 0 {
		return key[:index]
	}
	return key
}

func upstreamServerSelector(template string) *FieldSelectorSpec {
	return &FieldSelectorSpec{
		Template: template, Resource: "upstream.server", InventoryOwner: "upstream server manager",
		CompletionOwner: "upstream server inventory", IDValidator: "upstream server identity",
		IDEncoding: "bracket segment with RFC3986 percent-decoding",
	}
}

func llmProviderSelector(template string) *FieldSelectorSpec {
	return &FieldSelectorSpec{
		Template: template, Resource: "llm.provider", InventoryOwner: "LLM provider registry",
		CompletionOwner: "LLM provider inventory", IDValidator: "canonical LLM provider identity",
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
