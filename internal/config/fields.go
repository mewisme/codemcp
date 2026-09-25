package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
	"go.mewis.me/codemcp/internal/tunnel"
)

const RedactedValue = "<redacted>"

type FieldKind string

const (
	FieldBool     FieldKind = "bool"
	FieldInt      FieldKind = "int"
	FieldString   FieldKind = "string"
	FieldList     FieldKind = "list"
	FieldEnum     FieldKind = "enum"
	FieldReadOnly FieldKind = "readonly"
)

type SettingValueRole string

const (
	SettingValueConfigured SettingValueRole = "configured"
	SettingValueGenerated  SettingValueRole = "generated"
	SettingValueDerived    SettingValueRole = "derived"
	SettingValueInternal   SettingValueRole = "internal"
)

type FieldSpec struct {
	Key                string
	Label              string
	Section            FieldSection
	Description        string
	Details            string
	Kind               FieldKind
	Options            []string
	Values             []FieldValueSpec
	Editable           bool
	Sensitive          bool
	Guidance           string
	Related            []string
	ReadKey            string
	WriteKey           string
	Domain             string
	Readable           bool
	Writable           bool
	Secret             bool
	Derived            bool
	Clearable          bool
	DefaultReset       bool
	Rotatable          bool
	Revealable         bool
	Verifiable         bool
	ConfiguredStateKey string
	Presentation       SettingPresentationPolicy
	ApplicationOwner   string
	ScopedCommands     []string
	ScopedExemption    string
	InternalOnly       bool
	Virtual            bool
	Selector           *FieldSelectorSpec
	ValueRole          SettingValueRole
}

type FieldValueSpec struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type FieldSection string

const (
	FieldSectionRuntime      FieldSection = "runtime"
	FieldSectionAccess       FieldSection = "access"
	FieldSectionShell        FieldSection = "shell"
	FieldSectionIntegrations FieldSection = "integrations"
	FieldSectionTunnel       FieldSection = "tunnel"
)

type FieldState string

const (
	FieldStateDefault FieldState = "default"
	FieldStateCustom  FieldState = "custom"
	FieldStateManaged FieldState = "managed"
)

var fieldSpecs = []FieldSpec{
	{Key: "server.enabled", Label: "MCP HTTP server", Section: FieldSectionRuntime, Description: "controls whether the MCP HTTP transport is enabled", Details: "When disabled, clients cannot connect through the local HTTP MCP server. At least one MCP transport must remain enabled, so the Secure MCP Tunnel must be enabled before this can be disabled by itself.", Kind: FieldBool, Editable: true, Related: []string{"server.port", "server.expose.mode", "auth.mcp_enabled", "tunnel.enabled"}},
	{Key: "server.expose.mode", Label: "Exposure", Section: FieldSectionRuntime, Description: "controls which local network addresses expose the HTTP servers", Details: "Loopback access is always retained. Any non-loopback exposure requires server.allow_insecure_http=true and valid authentication for each enabled HTTP endpoint.", Kind: FieldEnum, Options: []string{"none", "all", "0.0.0.0", "interfaces"}, Values: []FieldValueSpec{{Value: "none", Description: "Bind only to loopback."}, {Value: "all", Description: "Bind loopback plus every eligible address discovered on all interfaces."}, {Value: "0.0.0.0", Description: "Bind one IPv4 wildcard listener and expose eligible IPv4 addresses."}, {Value: "interfaces", Description: "Bind loopback plus addresses from server.expose.interfaces."}}, Editable: true, Related: []string{"server.expose.interfaces", "server.allow_insecure_http", "auth.mcp_enabled", "auth.admin_enabled"}},
	{Key: "server.expose.interfaces", Label: "Exposure interfaces", Section: FieldSectionRuntime, Description: "lists network interfaces used when exposure mode is interfaces", Details: "Each name must resolve to an available interface with at least one eligible IP address at runtime. Duplicate names are removed and values are normalized before persistence.", Kind: FieldList, Editable: true, Guidance: "Set server.expose.mode=interfaces before relying on this list.", Related: []string{"server.expose.mode", "server.allow_insecure_http"}},
	{Key: "server.port", Label: "MCP HTTP port", Section: FieldSectionRuntime, Description: "sets the TCP port for the MCP HTTP server", Details: "Valid range is 1-65535. When both MCP and admin HTTP servers are enabled, their ports must differ.", Kind: FieldInt, Editable: true, Related: []string{"server.enabled", "admin.port"}},
	{Key: "server.allow_insecure_http", Label: "Allow insecure HTTP", Section: FieldSectionRuntime, Description: "allows authenticated plain HTTP endpoints beyond loopback", Details: "This opt-in is required for non-loopback exposure. It does not disable authentication requirements; exposed enabled endpoints still require configured credentials. Prefer the Secure MCP Tunnel or a TLS reverse proxy when possible.", Kind: FieldBool, Editable: true, Related: []string{"server.expose.mode", "auth.mcp_enabled", "auth.admin_enabled", "tunnel.enabled"}},
	{Key: "server.allow_unauthenticated_loopback", Label: "Allow unauthenticated loopback", Section: FieldSectionRuntime, Description: "WARNING: acknowledges intentionally disabling MCP/Admin HTTP authentication on loopback", Details: "Required before auth.mcp_enabled or auth.admin_enabled can be turned off while the corresponding HTTP server remains enabled. Valid only with server.expose.mode=none. Unauthenticated listeners accept any local process as a client; prefer keeping authentication enabled.", Kind: FieldBool, Editable: true, Guidance: "Set this only for trusted local development, then re-enable authentication promptly.", Related: []string{"auth.mcp_enabled", "auth.admin_enabled", "server.expose.mode", "server.enabled", "admin.enabled"}},
	{Key: "admin.enabled", Label: "Admin server", Section: FieldSectionRuntime, Description: "controls whether the admin HTTP server is enabled", Details: "When enabled, the admin endpoint listens using the configured admin port and the same network exposure policy. If admin authentication is enabled, a configured admin credential is required.", Kind: FieldBool, Editable: true, Related: []string{"admin.port", "auth.admin_enabled", "server.expose.mode"}},
	{Key: "admin.port", Label: "Admin port", Section: FieldSectionRuntime, Description: "sets the TCP port for the admin HTTP server", Details: "Valid range is 1-65535 while the admin server is enabled. When both HTTP servers are enabled, this port must differ from server.port.", Kind: FieldInt, Editable: true, Related: []string{"admin.enabled", "server.port"}},
	{Key: "auth.mcp_enabled", Label: "MCP authentication", Section: FieldSectionAccess, Description: "controls token authentication for the MCP HTTP endpoint", Details: "When the MCP HTTP server is enabled and this setting is true, an MCP credential must be configured. Disabling authentication while the MCP HTTP server remains enabled requires server.allow_unauthenticated_loopback=true and server.expose.mode=none. Non-loopback HTTP exposure always requires MCP authentication with a configured credential.", Kind: FieldBool, Editable: true, Related: []string{"auth.mcp_token_hash", "server.enabled", "server.expose.mode", "server.allow_unauthenticated_loopback"}},
	{Key: "auth.mcp_legacy_bearer", Label: "Legacy MCP bearer", Section: FieldSectionAccess, Description: "allows the existing static MCP token as a compatibility bearer credential", Details: "OAuth is canonical for protected HTTP/SSE MCP transports. Keep this enabled during migration for clients that still send the managed MCP token directly, then disable it once all clients use OAuth.", Kind: FieldBool, Editable: true, Related: []string{"auth.mcp_enabled", "auth.mcp_token_hash"}},
	{Key: "auth.admin_enabled", Label: "Admin authentication", Section: FieldSectionAccess, Description: "controls token authentication for the admin HTTP endpoint", Details: "When the admin server is enabled and this setting is true, an admin credential must be configured. Disabling authentication while the admin server remains enabled requires server.allow_unauthenticated_loopback=true and server.expose.mode=none. Non-loopback exposure with the admin endpoint enabled always requires admin authentication.", Kind: FieldBool, Editable: true, Related: []string{"auth.admin_token_hash", "admin.enabled", "server.expose.mode", "server.allow_unauthenticated_loopback"}},
	{Key: "auth.mcp_token_hash", Label: "MCP credential", Section: FieldSectionAccess, Description: "stores the managed credential hash used by MCP HTTP authentication", Details: "The raw token is never exposed through config views. This field is managed by the MCP authentication workflow and is not directly editable through config set.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage this credential with the MCP auth token workflow.", Related: []string{"auth.mcp_enabled", "server.enabled"}},
	{Key: "auth.admin_token_hash", Label: "Admin credential", Section: FieldSectionAccess, Description: "stores the managed credential hash used by admin HTTP authentication", Details: "The raw token is never exposed through config views. This field is managed by the admin authentication workflow and is not directly editable through config set.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage this credential with the admin auth token workflow.", Related: []string{"auth.admin_enabled", "admin.enabled"}},
	{Key: "permissions.allow_dirs", Label: "Allowed directories", Section: FieldSectionAccess, Description: "adds global filesystem roots that registered workspaces may access", Details: "These roots extend workspace-local access for filesystem and shell operations. Paths must be absolute, are normalized, and apply globally in addition to per-workspace allowed directories.", Kind: FieldList, Editable: true},
	{Key: "shell.path", Label: "Executable search paths", Section: FieldSectionShell, Description: "prepends additional executable directories to PATH for managed shell commands", Details: "Paths must be absolute. Configured entries are prepended to the inherited process PATH for foreground and background shell execution.", Kind: FieldList, Editable: true},
	{Key: "integrations.ponytail.active", Label: "Ponytail active", Section: FieldSectionIntegrations, Description: "controls whether Ponytail guidance is active by default", Details: "Ponytail biases coding work toward the smallest correct solution: reuse existing code, prefer standard/platform features, avoid speculative abstractions, and minimize unnecessary implementation.", Kind: FieldBool, Editable: true, Related: []string{"integrations.ponytail.mode"}},
	{Key: "integrations.ponytail.mode", Label: "Ponytail mode", Section: FieldSectionIntegrations, Description: "sets the default Ponytail intensity", Details: "This persisted value selects the default runtime intensity when Ponytail is active. Session-only modes such as review/off are not valid persisted values.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Build the requested solution but point out a simpler alternative when useful."}, {Value: "full", Description: "Enforce the reuse/stdlib/native-first ladder and prefer the shortest correct implementation."}, {Value: "ultra", Description: "Apply aggressive YAGNI pressure, favor deletion or minimal implementation, and challenge unnecessary scope."}}, Editable: true, Related: []string{"integrations.ponytail.active"}},
	{Key: "integrations.caveman.active", Label: "Caveman active", Section: FieldSectionIntegrations, Description: "controls whether Caveman response style is active by default", Details: "Caveman compresses assistant prose while preserving technical meaning, exact code, commands, numbers, and safety-critical clarity.", Kind: FieldBool, Editable: true, Related: []string{"integrations.caveman.mode"}},
	{Key: "integrations.caveman.mode", Label: "Caveman mode", Section: FieldSectionIntegrations, Description: "sets the default Caveman response intensity and language register", Details: "The persisted mode controls how aggressively response prose is compressed. The wenyan variants use progressively stronger classical Chinese compression. Session-only aliases such as off or wenyan are not persisted modes.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Remove filler and hedging while keeping normal professional sentences."}, {Value: "full", Description: "Use terse fragments where clear and aggressively remove nonessential prose."}, {Value: "ultra", Description: "Maximize compression while preserving unambiguous technical meaning."}, {Value: "wenyan-lite", Description: "Use a semi-classical Chinese register with moderate compression."}, {Value: "wenyan-full", Description: "Use strongly compressed classical Chinese sentence patterns."}, {Value: "wenyan-ultra", Description: "Use extreme classical Chinese abbreviation while retaining meaning."}}, Editable: true, Related: []string{"integrations.caveman.active"}},
	{Key: "integrations.rtk.enabled", Label: "RTK enabled", Section: FieldSectionIntegrations, Description: "controls whether RTK executable resolution is active", Details: "When enabled, CodeMCP resolves RTK in deterministic order: an explicitly configured executable, the system PATH, then a checksum-verified managed asset. Command rewriting is handled separately by the shell integration runtime.", Kind: FieldBool, Editable: true, Related: []string{"integrations.rtk.path"}},
	{Key: "integrations.rtk.path", Label: "RTK executable", Section: FieldSectionIntegrations, Description: "sets an explicit RTK executable path", Details: "Leave empty to resolve RTK from PATH and then the verified managed asset. A configured value must be an absolute path; runtime resolution validates that it is a non-empty executable file before use.", Kind: FieldString, Editable: true, Related: []string{"integrations.rtk.enabled"}},
	{Key: "integrations.codegraph.enabled", Label: "CodeGraph enabled", Section: FieldSectionIntegrations, Description: "controls whether CodeGraph runtime resolution is active", Details: "Disabled by default. When enabled, CodeMCP resolves an explicitly configured executable, then the system PATH, then a checksum-verified managed CodeGraph asset.", Kind: FieldBool, Editable: true, Related: []string{"integrations.codegraph.path"}},
	{Key: "integrations.codegraph.path", Label: "CodeGraph executable", Section: FieldSectionIntegrations, Description: "sets an explicit CodeGraph executable path", Details: "Leave empty to use system/managed resolution. A configured value must be absolute; execution remains bounded and requires an explicit workspace directory.", Kind: FieldString, Editable: true, Related: []string{"integrations.codegraph.enabled"}},
	{Key: "tunnel.enabled", Label: "Tunnel", Section: FieldSectionTunnel, Description: "controls whether the OpenAI Secure MCP Tunnel transport is enabled", Details: "An enabled tunnel requires both tunnel.id and a configured runtime API key. The tunnel can satisfy the requirement that at least one MCP transport remains enabled when the local MCP HTTP server is disabled.", Kind: FieldBool, Editable: true, Related: []string{"tunnel.id", "tunnel.api_key", "server.enabled"}},
	{Key: "tunnel.id", Label: "Tunnel ID", Section: FieldSectionTunnel, Description: "identifies the OpenAI Secure MCP Tunnel used by this runtime", Details: "The ID is required when the tunnel transport is enabled and is used together with the runtime API key to connect to the configured tunnel.", Kind: FieldString, Editable: true, Related: []string{"tunnel.enabled", "tunnel.api_key"}},
	{Key: "tunnel.api_key", Label: "Runtime API key", Section: FieldSectionTunnel, Description: "stores the managed runtime credential used to connect to the Secure MCP Tunnel", Details: "The raw runtime key is stored through the secret workflow and is redacted from config views. A configured runtime key is required when the tunnel transport is enabled.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage the runtime key from the Tunnel page.", Related: []string{"tunnel.enabled", "tunnel.id"}},
	{Key: "tunnel.admin.enabled", Label: "Tunnel administration", Section: FieldSectionTunnel, Description: "controls whether configured tunnel admin credentials may be used for management operations", Details: "Disabling tunnel administration preserves the configured admin key and scope while blocking normal managed-tunnel operations. Explicit verification and diagnostics remain available.", Kind: FieldBool, Editable: true, Related: []string{"tunnel.admin.key", "tunnel.admin.verified"}},
	{Key: "tunnel.admin.key", Label: "Admin key", Section: FieldSectionTunnel, Description: "stores the configured admin credential used for tunnel control-plane operations", Details: "The admin key is separate from the runtime tunnel key. Saving it does not contact the control plane; verification is an explicit operation. The raw key is stored in the secret store and redacted from normal config views.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Configure the key and exactly one admin scope, then verify explicitly.", Related: []string{"tunnel.admin.organization_id", "tunnel.admin.workspace_id", "tunnel.admin.tenant_id", "tunnel.admin.verified"}},
	{Key: "tunnel.admin.organization_id", Label: "Admin organization scope", Section: FieldSectionTunnel, Description: "configures the organization scope for tunnel administration", Details: "Exactly one organization, workspace, or tenant scope is active at a time. Setting this value clears the configured workspace and tenant scopes and invalidates any previous verification result.", Kind: FieldString, Editable: true, Guidance: "Configure exactly one admin scope before verification.", Related: []string{"tunnel.admin.key", "tunnel.admin.workspace_id", "tunnel.admin.tenant_id", "tunnel.admin.verified"}},
	{Key: "tunnel.admin.workspace_id", Label: "Admin workspace scope", Section: FieldSectionTunnel, Description: "configures the workspace scope for tunnel administration", Details: "Exactly one organization, workspace, or tenant scope is active at a time. Setting this value clears the configured organization and tenant scopes and invalidates any previous verification result.", Kind: FieldString, Editable: true, Guidance: "Configure exactly one admin scope before verification.", Related: []string{"tunnel.admin.key", "tunnel.admin.organization_id", "tunnel.admin.tenant_id", "tunnel.admin.verified"}},
	{Key: "tunnel.admin.tenant_id", Label: "Admin tenant scope", Section: FieldSectionTunnel, Description: "configures the tenant scope for tunnel administration", Details: "Exactly one organization, workspace, or tenant scope is active at a time. Setting this value clears the configured organization and workspace scopes and invalidates any previous verification result.", Kind: FieldString, Editable: true, Guidance: "Configure exactly one admin scope before verification.", Related: []string{"tunnel.admin.key", "tunnel.admin.organization_id", "tunnel.admin.workspace_id", "tunnel.admin.verified"}},
	{Key: "tunnel.admin.verified", Label: "Tunnel admin verified", Section: FieldSectionTunnel, Description: "reports whether the configured admin key and scope were explicitly verified", Details: "This derived state is set only by the explicit verification operation and is invalidated whenever the configured admin key or scope changes.", Kind: FieldBool},
	{Key: "tunnel.admin.read_access", Label: "Tunnel admin Read access", Section: FieldSectionTunnel, Description: "reports verified tunnel admin read access", Details: "This read-only derived state reflects the last successful explicit verification and is cleared when the configured key or scope changes.", Kind: FieldBool},
	{Key: "tunnel.admin.manage_access", Label: "Tunnel admin Manage access", Section: FieldSectionTunnel, Description: "reports verified tunnel admin manage access", Details: "This read-only derived state reflects the last successful explicit verification and is cleared when the configured key or scope changes.", Kind: FieldBool},
	{Key: "tunnel.control_plane_base_url", Label: "Control-plane URL", Section: FieldSectionTunnel, Description: "overrides the OpenAI tunnel control-plane base URL", Details: "When empty, the tunnel client uses its default control-plane endpoint. A custom value must be an absolute HTTP or HTTPS URL with a host.", Kind: FieldString, Editable: true, Guidance: "Leave empty unless a different control-plane endpoint is explicitly required.", Related: []string{"tunnel.enabled", "tunnel.id"}},
	{Key: "tunnel.organization_id", Label: "Organization ID", Section: FieldSectionTunnel, Description: "sets the OpenAI organization context associated with tunnel runtime operations", Details: "This optional organization identifier is carried in tunnel runtime configuration and is distinct from the verified admin-key organization scope.", Kind: FieldString, Editable: true, Related: []string{"tunnel.admin.organization_id", "tunnel.enabled"}},
}

func Fields() []FieldSpec {
	result := make([]FieldSpec, len(fieldSpecs))
	for index, spec := range fieldSpecs {
		result[index] = cloneFieldSpec(spec)
	}
	return result
}

func FieldByKey(key string) (FieldSpec, bool) {
	key = canonicalFieldKey(key)
	for _, spec := range fieldSpecs {
		if spec.Key == key {
			return cloneFieldSpec(spec), true
		}
	}
	return FieldSpec{}, false
}

func cloneFieldSpec(spec FieldSpec) FieldSpec {
	spec.Options = append([]string(nil), spec.Options...)
	spec.Values = append([]FieldValueSpec(nil), spec.Values...)
	spec.Related = append([]string(nil), spec.Related...)
	spec.ScopedCommands = append([]string(nil), spec.ScopedCommands...)
	if spec.Selector != nil {
		selector := *spec.Selector
		spec.Selector = &selector
	}
	return spec
}

func SetValue(cfg *Config, key, raw string) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	key = canonicalFieldKey(key)
	switch key {
	case "server.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Server.Enabled = value
	case "server.expose":
		value, err := ParseExposure(raw)
		if err != nil {
			return err
		}
		cfg.Server.Expose = value
	case "server.expose.mode":
		mode := ExposureMode(strings.ToLower(strings.TrimSpace(raw)))
		if mode != ExposureNone && mode != ExposureAll && mode != ExposureWildcard && mode != ExposureInterfaces {
			return errors.New("server.expose.mode must be none, all, 0.0.0.0, or interfaces")
		}
		cfg.Server.Expose.Mode = mode
		cfg.Server.Expose = NormalizeExposure(cfg.Server.Expose)
	case "server.expose.interfaces":
		interfaces := splitFieldList(raw)
		if len(interfaces) == 0 {
			cfg.Server.Expose = ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}
		} else {
			cfg.Server.Expose = NormalizeExposure(ExposureConfig{Mode: ExposureInterfaces, Interfaces: interfaces})
		}
	case "server.port":
		value, err := parseIntField(raw, key)
		if err != nil {
			return err
		}
		cfg.Server.Port = value
	case "server.allow_insecure_http":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Server.AllowInsecureHTTP = value
	case "server.allow_unauthenticated_loopback":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Server.AllowUnauthenticatedLoopback = value
	case "admin.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Admin.Enabled = value
	case "admin.port":
		value, err := parseIntField(raw, key)
		if err != nil {
			return err
		}
		cfg.Admin.Port = value
	case "auth.mcp_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Auth.MCPEnabled = value
	case "auth.mcp_legacy_bearer":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Auth.MCPLegacyBearer = value
	case "auth.admin_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Auth.AdminEnabled = value
	case "permissions.allow_dirs":
		cfg.Permissions.AllowDirs = splitFieldList(raw)
	case "shell.path":
		cfg.Shell.Path = splitFieldList(raw)
	case "integrations.ponytail.active":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.Ponytail.Active = value
	case "integrations.ponytail.mode":
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "lite" && value != "full" && value != "ultra" {
			return errors.New("integrations.ponytail.mode must be lite, full, or ultra")
		}
		cfg.Integrations.Ponytail.Mode = value
	case "integrations.caveman.active":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.Caveman.Active = value
	case "integrations.caveman.mode":
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra":
			cfg.Integrations.Caveman.Mode = value
		default:
			return errors.New("integrations.caveman.mode must be lite, full, ultra, wenyan-lite, wenyan-full, or wenyan-ultra")
		}
	case "integrations.rtk.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.RTK.Enabled = value
	case "integrations.rtk.path":
		cfg.Integrations.RTK.Path = strings.TrimSpace(raw)
	case "integrations.codegraph.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.CodeGraph.Enabled = value
	case "integrations.codegraph.path":
		cfg.Integrations.CodeGraph.Path = strings.TrimSpace(raw)
	case "tunnel.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Tunnel.Enabled = value
	case "tunnel.id":
		cfg.Tunnel.ID = raw
	case "tunnel.api_key":
		cfg.Tunnel.APIKey = raw
	case "tunnel.admin.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		tunnel.SetAdminEnabled(&cfg.Tunnel, value)
	case "tunnel.admin.key":
		return errors.New("tunnel admin key must be set through the canonical secret setting service")
	case "tunnel.admin.organization_id":
		return tunnel.SetAdminScope(&cfg.Tunnel, tunnel.AdminScope{OrganizationID: strings.TrimSpace(raw)})
	case "tunnel.admin.workspace_id":
		return tunnel.SetAdminScope(&cfg.Tunnel, tunnel.AdminScope{WorkspaceID: strings.TrimSpace(raw)})
	case "tunnel.admin.tenant_id":
		return tunnel.SetAdminScope(&cfg.Tunnel, tunnel.AdminScope{TenantID: strings.TrimSpace(raw)})
	case "tunnel.control_plane_base_url":
		cfg.Tunnel.ControlPlaneBaseURL = raw
	case "tunnel.organization_id":
		cfg.Tunnel.OrganizationID = raw
	case "auth.mcp_token_hash", "auth.admin_token_hash":
		return errors.New("token hashes cannot be set through config; use cm auth <mcp|admin> create")
	default:
		return fmt.Errorf("unsupported config key: %s", key)
	}
	return nil
}

func SetValueValidated(cfg *Config, key, raw string) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	next := *cfg
	if err := SetValue(&next, key, raw); err != nil {
		return err
	}
	if err := Validate(next); err != nil {
		return err
	}
	*cfg = next
	return nil
}

func RawValue(cfg Config, key string) (string, error) {
	key = canonicalFieldKey(key)
	switch key {
	case "server.enabled":
		return strconv.FormatBool(cfg.Server.Enabled), nil
	case "server.expose":
		exposure := NormalizeExposure(cfg.Server.Expose)
		if exposure.Mode == ExposureInterfaces {
			return strings.Join(exposure.Interfaces, ","), nil
		}
		return string(exposure.Mode), nil
	case "server.expose.mode":
		return string(NormalizeExposure(cfg.Server.Expose).Mode), nil
	case "server.expose.interfaces":
		return strings.Join(NormalizeExposure(cfg.Server.Expose).Interfaces, ","), nil
	case "server.port":
		return strconv.Itoa(cfg.Server.Port), nil
	case "server.allow_insecure_http":
		return strconv.FormatBool(cfg.Server.AllowInsecureHTTP), nil
	case "server.allow_unauthenticated_loopback":
		return strconv.FormatBool(cfg.Server.AllowUnauthenticatedLoopback), nil
	case "admin.enabled":
		return strconv.FormatBool(cfg.Admin.Enabled), nil
	case "admin.port":
		return strconv.Itoa(cfg.Admin.Port), nil
	case "auth.mcp_enabled":
		return strconv.FormatBool(cfg.Auth.MCPEnabled), nil
	case "auth.mcp_legacy_bearer":
		return strconv.FormatBool(cfg.Auth.MCPLegacyBearer), nil
	case "auth.admin_enabled":
		return strconv.FormatBool(cfg.Auth.AdminEnabled), nil
	case "auth.mcp_token_hash":
		return cfg.Auth.MCPTokenHash, nil
	case "auth.admin_token_hash":
		return cfg.Auth.AdminTokenHash, nil
	case "permissions.allow_dirs":
		return strings.Join(cfg.Permissions.AllowDirs, ","), nil
	case "shell.path":
		return strings.Join(cfg.Shell.Path, ","), nil
	case "integrations.ponytail.active":
		return strconv.FormatBool(cfg.Integrations.Ponytail.Active), nil
	case "integrations.ponytail.mode":
		return cfg.Integrations.Ponytail.Mode, nil
	case "integrations.caveman.active":
		return strconv.FormatBool(cfg.Integrations.Caveman.Active), nil
	case "integrations.caveman.mode":
		return cfg.Integrations.Caveman.Mode, nil
	case "integrations.rtk.enabled":
		return strconv.FormatBool(cfg.Integrations.RTK.Enabled), nil
	case "integrations.rtk.path":
		return cfg.Integrations.RTK.Path, nil
	case "integrations.codegraph.enabled":
		return strconv.FormatBool(cfg.Integrations.CodeGraph.Enabled), nil
	case "integrations.codegraph.path":
		return cfg.Integrations.CodeGraph.Path, nil
	case "tunnel.enabled":
		return strconv.FormatBool(cfg.Tunnel.Enabled), nil
	case "tunnel.id":
		return cfg.Tunnel.ID, nil
	case "tunnel.api_key":
		return cfg.Tunnel.APIKey, nil
	case "tunnel.admin.enabled":
		return strconv.FormatBool(tunnel.AdminEnabled(cfg.Tunnel)), nil
	case "tunnel.admin.key":
		return cfg.Tunnel.Admin.Key, nil
	case "tunnel.admin.organization_id":
		return cfg.Tunnel.Admin.OrganizationID, nil
	case "tunnel.admin.workspace_id":
		return cfg.Tunnel.Admin.WorkspaceID, nil
	case "tunnel.admin.tenant_id":
		return cfg.Tunnel.Admin.TenantID, nil
	case "tunnel.admin.verified":
		return strconv.FormatBool(tunnel.AdminVerified(cfg.Tunnel)), nil
	case "tunnel.admin.read_access":
		return strconv.FormatBool(cfg.Tunnel.Admin.ReadAccess), nil
	case "tunnel.admin.manage_access":
		return strconv.FormatBool(cfg.Tunnel.Admin.ManageAccess), nil
	case "tunnel.control_plane_base_url":
		return cfg.Tunnel.ControlPlaneBaseURL, nil
	case "tunnel.organization_id":
		return cfg.Tunnel.OrganizationID, nil
	default:
		return "", fmt.Errorf("unsupported config key: %s", key)
	}
}

func DisplayValue(cfg Config, spec FieldSpec) (string, error) {
	value, err := RawValue(cfg, spec.Key)
	if err != nil {
		return "", err
	}
	if spec.Sensitive {
		if strings.TrimSpace(value) == "" {
			return "not configured", nil
		}
		return "configured", nil
	}
	if spec.Kind == FieldList && strings.TrimSpace(value) == "" {
		return "none", nil
	}
	if strings.TrimSpace(value) == "" {
		return "-", nil
	}
	return value, nil
}

func State(cfg Config, spec FieldSpec) (FieldState, error) {
	if !spec.Editable || spec.Kind == FieldReadOnly {
		return FieldStateManaged, nil
	}
	current, err := comparableFieldValue(cfg, spec)
	if err != nil {
		return "", err
	}
	defaults := Default()
	baseline, err := comparableFieldValue(defaults, spec)
	if err != nil {
		return "", err
	}
	if current == baseline {
		return FieldStateDefault, nil
	}
	return FieldStateCustom, nil
}

func comparableFieldValue(cfg Config, spec FieldSpec) (string, error) {
	value, err := RawValue(cfg, spec.Key)
	if err != nil {
		return "", err
	}
	if spec.Sensitive {
		return "", nil
	}
	if spec.Kind != FieldList {
		return strings.TrimSpace(value), nil
	}
	items := splitFieldList(value)
	slices.Sort(items)
	return strings.Join(items, "\x00"), nil
}

func RedactedTree(cfg Config) (map[string]any, error) {
	data, err := configformat.Marshal(configformat.JSON, cfg)
	if err != nil {
		return nil, err
	}
	value, err := configformat.DecodeGeneric(configformat.JSON, data)
	if err != nil {
		return nil, err
	}
	tree, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("config view is not an object")
	}
	setTreeValue(tree, "auth.mcp_token_hash", RedactedValue)
	setTreeValue(tree, "auth.admin_token_hash", RedactedValue)
	setTreeValue(tree, "tunnel.api_key", RedactedValue)
	setTreeValue(tree, "tunnel.admin.key", RedactedValue)
	return tree, nil
}

func RedactedValueAt(cfg Config, key string) (any, error) {
	key = canonicalFieldKey(key)
	tree, err := RedactedTree(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return tree, nil
	}
	var current any = tree
	for _, part := range strings.Split(key, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("config key has no children: %s", key)
		}
		next, exists := object[part]
		if !exists {
			return nil, fmt.Errorf("unsupported config key: %s", key)
		}
		current = next
	}
	return current, nil
}

func canonicalFieldKey(key string) string {
	return strings.TrimSpace(key)
}

func setTreeValue(tree map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	current := tree
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func splitFieldList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", ",")
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parseBoolField(raw, key string) (bool, error) {
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value, nil
}

func parseIntField(raw, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}
