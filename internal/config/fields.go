package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"go.mewis.me/codemcp/internal/configformat"
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

type FieldSpec struct {
	Key         string
	Label       string
	Section     FieldSection
	Description string
	Details     string
	Kind        FieldKind
	Options     []string
	Values      []FieldValueSpec
	Editable    bool
	Sensitive   bool
	Guidance    string
	Related     []string
}

type FieldValueSpec struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type FieldSection string

const (
	FieldSectionRuntime  FieldSection = "runtime"
	FieldSectionAccess   FieldSection = "access"
	FieldSectionShell    FieldSection = "shell"
	FieldSectionFeatures FieldSection = "features"
	FieldSectionTunnel   FieldSection = "tunnel"
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
	{Key: "features.ponytail.active", Label: "Ponytail active", Section: FieldSectionFeatures, Description: "controls whether Ponytail guidance is active by default", Details: "Ponytail biases coding work toward the smallest correct solution: reuse existing code, prefer standard/platform features, avoid speculative abstractions, and minimize unnecessary implementation.", Kind: FieldBool, Editable: true, Related: []string{"features.ponytail.mode"}},
	{Key: "features.ponytail.mode", Label: "Ponytail mode", Section: FieldSectionFeatures, Description: "sets the default Ponytail intensity", Details: "This persisted value selects the default runtime intensity when Ponytail is active. Session-only modes such as review/off are not valid persisted values.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Build the requested solution but point out a simpler alternative when useful."}, {Value: "full", Description: "Enforce the reuse/stdlib/native-first ladder and prefer the shortest correct implementation."}, {Value: "ultra", Description: "Apply aggressive YAGNI pressure, favor deletion or minimal implementation, and challenge unnecessary scope."}}, Editable: true, Related: []string{"features.ponytail.active"}},
	{Key: "features.caveman.active", Label: "Caveman active", Section: FieldSectionFeatures, Description: "controls whether Caveman response style is active by default", Details: "Caveman compresses assistant prose while preserving technical meaning, exact code, commands, numbers, and safety-critical clarity.", Kind: FieldBool, Editable: true, Related: []string{"features.caveman.mode"}},
	{Key: "features.caveman.mode", Label: "Caveman mode", Section: FieldSectionFeatures, Description: "sets the default Caveman response intensity and language register", Details: "The persisted mode controls how aggressively response prose is compressed. The wenyan variants use progressively stronger classical Chinese compression. Session-only aliases such as off or wenyan are not persisted modes.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Remove filler and hedging while keeping normal professional sentences."}, {Value: "full", Description: "Use terse fragments where clear and aggressively remove nonessential prose."}, {Value: "ultra", Description: "Maximize compression while preserving unambiguous technical meaning."}, {Value: "wenyan-lite", Description: "Use a semi-classical Chinese register with moderate compression."}, {Value: "wenyan-full", Description: "Use strongly compressed classical Chinese sentence patterns."}, {Value: "wenyan-ultra", Description: "Use extreme classical Chinese abbreviation while retaining meaning."}}, Editable: true, Related: []string{"features.caveman.active"}},
	{Key: "tunnel.enabled", Label: "Tunnel", Section: FieldSectionTunnel, Description: "controls whether the OpenAI Secure MCP Tunnel transport is enabled", Details: "An enabled tunnel requires both tunnel.id and a configured runtime API key. The tunnel can satisfy the requirement that at least one MCP transport remains enabled when the local MCP HTTP server is disabled.", Kind: FieldBool, Editable: true, Related: []string{"tunnel.id", "tunnel.api_key", "server.enabled"}},
	{Key: "tunnel.id", Label: "Tunnel ID", Section: FieldSectionTunnel, Description: "identifies the OpenAI Secure MCP Tunnel used by this runtime", Details: "The ID is required when the tunnel transport is enabled and is used together with the runtime API key to connect to the configured tunnel.", Kind: FieldString, Editable: true, Related: []string{"tunnel.enabled", "tunnel.api_key"}},
	{Key: "tunnel.api_key", Label: "Runtime API key", Section: FieldSectionTunnel, Description: "stores the managed runtime credential used to connect to the Secure MCP Tunnel", Details: "The raw runtime key is stored through the secret workflow and is redacted from config views. A configured runtime key is required when the tunnel transport is enabled.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage the runtime key from the Tunnel page.", Related: []string{"tunnel.enabled", "tunnel.id"}},
	{Key: "tunnel.admin_key", Label: "Admin key", Section: FieldSectionTunnel, Description: "stores the managed admin credential used for tunnel control-plane operations", Details: "The admin key is separate from the runtime tunnel key. It is used for management operations such as listing, creating, updating, or deleting managed tunnels and is redacted from config views.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage and verify the admin key from the Tunnel page.", Related: []string{"tunnel.admin_organization_id", "tunnel.admin_workspace_id", "tunnel.admin_tenant_id"}},
	{Key: "tunnel.admin_organization_id", Label: "Admin organization scope", Section: FieldSectionTunnel, Description: "records the verified organization scope for the tunnel admin key", Details: "This read-only value is populated from admin-key verification and constrains tunnel management operations to the verified organization scope when present.", Kind: FieldReadOnly, Guidance: "This scope is set only after admin-key verification.", Related: []string{"tunnel.admin_key", "tunnel.admin_workspace_id", "tunnel.admin_tenant_id"}},
	{Key: "tunnel.admin_workspace_id", Label: "Admin workspace scope", Section: FieldSectionTunnel, Description: "records the verified workspace scope for the tunnel admin key", Details: "This read-only value is populated from admin-key verification and constrains tunnel management operations to the verified workspace scope when present.", Kind: FieldReadOnly, Guidance: "This scope is set only after admin-key verification.", Related: []string{"tunnel.admin_key", "tunnel.admin_organization_id", "tunnel.admin_tenant_id"}},
	{Key: "tunnel.admin_tenant_id", Label: "Admin tenant scope", Section: FieldSectionTunnel, Description: "records the verified tenant scope for the tunnel admin key", Details: "This read-only value is populated from admin-key verification and constrains tunnel management operations to the verified tenant scope when present.", Kind: FieldReadOnly, Guidance: "This scope is set only after admin-key verification.", Related: []string{"tunnel.admin_key", "tunnel.admin_organization_id", "tunnel.admin_workspace_id"}},
	{Key: "tunnel.control_plane_base_url", Label: "Control-plane URL", Section: FieldSectionTunnel, Description: "overrides the OpenAI tunnel control-plane base URL", Details: "When empty, the tunnel client uses its default control-plane endpoint. A custom value must be an absolute HTTP or HTTPS URL with a host.", Kind: FieldString, Editable: true, Guidance: "Leave empty unless a different control-plane endpoint is explicitly required.", Related: []string{"tunnel.enabled", "tunnel.id"}},
	{Key: "tunnel.organization_id", Label: "Organization ID", Section: FieldSectionTunnel, Description: "sets the OpenAI organization context associated with tunnel runtime operations", Details: "This optional organization identifier is carried in tunnel runtime configuration and is distinct from the verified admin-key organization scope.", Kind: FieldString, Editable: true, Related: []string{"tunnel.admin_organization_id", "tunnel.enabled"}},
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
		cfg.Server.Expose = NormalizeExposure(ExposureConfig{Mode: ExposureInterfaces, Interfaces: splitFieldList(raw)})
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
	case "features.ponytail.active":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Features.Ponytail.Active = value
	case "features.ponytail.mode":
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "lite" && value != "full" && value != "ultra" {
			return errors.New("features.ponytail.mode must be lite, full, or ultra")
		}
		cfg.Features.Ponytail.Mode = value
	case "features.caveman.active":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Features.Caveman.Active = value
	case "features.caveman.mode":
		value := strings.ToLower(strings.TrimSpace(raw))
		switch value {
		case "lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra":
			cfg.Features.Caveman.Mode = value
		default:
			return errors.New("features.caveman.mode must be lite, full, ultra, wenyan-lite, wenyan-full, or wenyan-ultra")
		}
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
	case "tunnel.admin_key", "tunnel.admin_organization_id", "tunnel.admin_workspace_id", "tunnel.admin_tenant_id":
		return errors.New("tunnel admin credentials cannot be set through config; use cm tunnel admin key")
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
	case "features.ponytail.active":
		return strconv.FormatBool(cfg.Features.Ponytail.Active), nil
	case "features.ponytail.mode":
		return cfg.Features.Ponytail.Mode, nil
	case "features.caveman.active":
		return strconv.FormatBool(cfg.Features.Caveman.Active), nil
	case "features.caveman.mode":
		return cfg.Features.Caveman.Mode, nil
	case "tunnel.enabled":
		return strconv.FormatBool(cfg.Tunnel.Enabled), nil
	case "tunnel.id":
		return cfg.Tunnel.ID, nil
	case "tunnel.api_key":
		return cfg.Tunnel.APIKey, nil
	case "tunnel.admin_key":
		return cfg.Tunnel.AdminKey, nil
	case "tunnel.admin_organization_id":
		return cfg.Tunnel.AdminOrganizationID, nil
	case "tunnel.admin_workspace_id":
		return cfg.Tunnel.AdminWorkspaceID, nil
	case "tunnel.admin_tenant_id":
		return cfg.Tunnel.AdminTenantID, nil
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
	setTreeValue(tree, "tunnel.admin_key", RedactedValue)
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
	key = strings.TrimSpace(key)
	switch key {
	case "features.ponytail.enabled":
		return "features.ponytail.active"
	case "features.caveman.enabled":
		return "features.caveman.active"
	default:
		return key
	}
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
