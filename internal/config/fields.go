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
	Input              FieldInputSpec
}

type FieldInputSpec struct {
	Shape     string `json:"shape,omitempty"`
	ItemShape string `json:"item_shape,omitempty"`
	MinInt    int    `json:"min_int,omitempty"`
	MaxInt    int    `json:"max_int,omitempty"`
	HasMinInt bool   `json:"has_min_int,omitempty"`
	HasMaxInt bool   `json:"has_max_int,omitempty"`
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
	{Key: "http.mcp.enabled", Label: "MCP HTTP server", Section: FieldSectionRuntime, Description: "controls whether the MCP HTTP transport is enabled", Details: "When disabled, clients cannot connect through the local HTTP MCP server. At least one MCP transport must remain enabled, so the Secure MCP Tunnel must be enabled before this can be disabled by itself.", Kind: FieldBool, Editable: true, Related: []string{"http.mcp.port", "http.exposure.mode", "http.mcp.auth.enabled", "tunnel.enabled"}},
	{Key: "http.exposure.mode", Label: "Exposure", Section: FieldSectionRuntime, Description: "controls which local network addresses expose the HTTP servers", Details: "Loopback access is always retained. Any non-loopback exposure requires http.security.allow_insecure=true and valid authentication for each enabled HTTP endpoint.", Kind: FieldEnum, Options: []string{"none", "all", "0.0.0.0", "interfaces"}, Values: []FieldValueSpec{{Value: "none", Description: "Bind only to loopback."}, {Value: "all", Description: "Bind loopback plus every eligible address discovered on all interfaces."}, {Value: "0.0.0.0", Description: "Bind one IPv4 wildcard listener and expose eligible IPv4 addresses."}, {Value: "interfaces", Description: "Bind loopback plus addresses from http.exposure.interfaces."}}, Editable: true, Related: []string{"http.exposure.interfaces", "http.security.allow_insecure", "http.mcp.auth.enabled", "http.admin.auth.enabled"}},
	{Key: "http.exposure.interfaces", Label: "Exposure interfaces", Section: FieldSectionRuntime, Description: "lists network interfaces used when exposure mode is interfaces", Details: "Each name must resolve to an available interface with at least one eligible IP address at runtime. Duplicate names are removed and values are normalized before persistence.", Kind: FieldList, Editable: true, Input: FieldInputSpec{ItemShape: "interface"}, Guidance: "Set http.exposure.mode=interfaces before relying on this list.", Related: []string{"http.exposure.mode", "http.security.allow_insecure"}},
	{Key: "http.mcp.port", Label: "MCP HTTP port", Section: FieldSectionRuntime, Description: "sets the TCP port for the MCP HTTP server", Details: "Valid range is 1-65535. When both MCP and admin HTTP servers are enabled, their ports must differ.", Kind: FieldInt, Editable: true, Input: boundedIntInput(1, 65535), Related: []string{"http.mcp.enabled", "http.admin.port"}},
	{Key: "http.security.allow_insecure", Label: "Allow insecure HTTP", Section: FieldSectionRuntime, Description: "allows authenticated plain HTTP endpoints beyond loopback", Details: "This opt-in is required for non-loopback exposure. It does not disable authentication requirements; exposed enabled endpoints still require configured credentials. Prefer the Secure MCP Tunnel or a TLS reverse proxy when possible.", Kind: FieldBool, Editable: true, Related: []string{"http.exposure.mode", "http.mcp.auth.enabled", "http.admin.auth.enabled", "tunnel.enabled"}},
	{Key: "http.security.allow_unauthenticated_loopback", Label: "Allow unauthenticated loopback", Section: FieldSectionRuntime, Description: "WARNING: acknowledges intentionally disabling MCP/Admin HTTP authentication on loopback", Details: "Required before http.mcp.auth.enabled or http.admin.auth.enabled can be turned off while the corresponding HTTP server remains enabled. Valid only with http.exposure.mode=none. Unauthenticated listeners accept any local process as a client; prefer keeping authentication enabled.", Kind: FieldBool, Editable: true, Guidance: "Set this only for trusted local development, then re-enable authentication promptly.", Related: []string{"http.mcp.auth.enabled", "http.admin.auth.enabled", "http.exposure.mode", "http.mcp.enabled", "http.admin.enabled"}},
	{Key: "http.admin.enabled", Label: "Admin server", Section: FieldSectionRuntime, Description: "controls whether the admin HTTP server is enabled", Details: "When enabled, the admin endpoint listens using the configured admin port and the same network exposure policy. If admin authentication is enabled, a configured admin credential is required.", Kind: FieldBool, Editable: true, Related: []string{"http.admin.port", "http.admin.auth.enabled", "http.exposure.mode"}},
	{Key: "http.admin.port", Label: "Admin port", Section: FieldSectionRuntime, Description: "sets the TCP port for the admin HTTP server", Details: "Valid range is 1-65535 while the admin server is enabled. When both HTTP servers are enabled, this port must differ from http.mcp.port.", Kind: FieldInt, Editable: true, Input: boundedIntInput(1, 65535), Related: []string{"http.admin.enabled", "http.mcp.port"}},
	{Key: "http.mcp.auth.enabled", Label: "MCP authentication", Section: FieldSectionAccess, Description: "controls token authentication for the MCP HTTP endpoint", Details: "When the MCP HTTP server is enabled and this setting is true, an MCP credential must be configured. Disabling authentication while the MCP HTTP server remains enabled requires http.security.allow_unauthenticated_loopback=true and http.exposure.mode=none. Non-loopback HTTP exposure always requires MCP authentication with a configured credential.", Kind: FieldBool, Editable: true, Related: []string{"http.mcp.auth.token_hash", "http.mcp.enabled", "http.exposure.mode", "http.security.allow_unauthenticated_loopback"}},
	{Key: "http.mcp.auth.legacy_bearer", Label: "Legacy MCP bearer", Section: FieldSectionAccess, Description: "allows the existing static MCP token as a compatibility bearer credential", Details: "OAuth is canonical for protected HTTP/SSE MCP transports. Keep this enabled during migration for clients that still send the managed MCP token directly, then disable it once all clients use OAuth.", Kind: FieldBool, Editable: true, Related: []string{"http.mcp.auth.enabled", "http.mcp.auth.token_hash"}},
	{Key: "http.admin.auth.enabled", Label: "Admin authentication", Section: FieldSectionAccess, Description: "controls token authentication for the admin HTTP endpoint", Details: "When the admin server is enabled and this setting is true, an admin credential must be configured. Disabling authentication while the admin server remains enabled requires http.security.allow_unauthenticated_loopback=true and http.exposure.mode=none. Non-loopback exposure with the admin endpoint enabled always requires admin authentication.", Kind: FieldBool, Editable: true, Related: []string{"http.admin.auth.token_hash", "http.admin.enabled", "http.exposure.mode", "http.security.allow_unauthenticated_loopback"}},
	{Key: "http.mcp.auth.token_hash", Label: "MCP credential", Section: FieldSectionAccess, Description: "stores the managed credential hash used by MCP HTTP authentication", Details: "The raw token is never exposed through config views. This field is managed by the MCP authentication workflow and is not directly editable through config set.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage this credential with the MCP auth token workflow.", Related: []string{"http.mcp.auth.enabled", "http.mcp.enabled"}},
	{Key: "http.admin.auth.token_hash", Label: "Admin credential", Section: FieldSectionAccess, Description: "stores the managed credential hash used by admin HTTP authentication", Details: "The raw token is never exposed through config views. This field is managed by the admin authentication workflow and is not directly editable through config set.", Kind: FieldReadOnly, Sensitive: true, Guidance: "Manage this credential with the admin auth token workflow.", Related: []string{"http.admin.auth.enabled", "http.admin.enabled"}},
	{Key: "permissions.allow_dirs", Label: "Allowed directories", Section: FieldSectionAccess, Description: "adds global filesystem roots that registered workspaces may access", Details: "These roots extend workspace-local access for filesystem and shell operations. Paths must be absolute, are normalized, and apply globally in addition to per-workspace allowed directories.", Kind: FieldList, Editable: true, Input: FieldInputSpec{ItemShape: "absolute-path"}},
	{Key: "permissions.mcp_config_read", Label: "MCP agent config reads", Section: FieldSectionAccess, Description: "allows agent-facing MCP configuration read tools to access the global CodeMCP setting projection", Details: "Enabled by default. This exposes only the bounded non-secret global setting projection; MCP transport authentication and workspace access do not broaden that projection.", Kind: FieldBool, Editable: true},
	{Key: "permissions.mcp_config_write", Label: "MCP agent config writes", Section: FieldSectionAccess, Description: "allows the guarded agent-facing MCP configuration mutation workflow to target eligible global settings", Details: "Enabled by default. This grants eligibility only; config_set still requires mandatory local approval and cannot write managed secrets.", Kind: FieldBool, Editable: true, Related: []string{"permissions.mcp_config_read"}},
	{Key: "shell.path", Label: "Executable search paths", Section: FieldSectionShell, Description: "prepends additional executable directories to PATH for managed shell commands", Details: "Paths must be absolute. Configured entries are prepended to the inherited process PATH for foreground and background shell execution.", Kind: FieldList, Editable: true, Input: FieldInputSpec{ItemShape: "absolute-path"}},
	{Key: "telemetry.enabled", Label: "Anonymous product telemetry", Section: FieldSectionRuntime, Description: "controls privacy-bounded anonymous product usage telemetry", Details: "Enabled by default. CM_TELEMETRY overrides this persisted preference at runtime. This operator privacy preference is never exposed through agent-facing MCP config tools.", Kind: FieldBool, Editable: true},
	{Key: "telegram.enabled", Label: "Telegram interface", Section: FieldSectionRuntime, Description: "controls the Telegram bot runtime", Details: "Enabled by default. The bot token is stored separately in the secret store; the runtime starts only when a token exists and at least one authorized private user is configured.", Kind: FieldBool, Editable: true, Related: []string{"telegram.allowed_user_ids", "telegram.topics_enabled", "telegram.logs_mini_app.enabled"}},
	{Key: "telegram.allowed_user_ids", Label: "Telegram authorized users", Section: FieldSectionAccess, Description: "lists Telegram user IDs allowed to invoke the private administration interface", Details: "Only positive numeric user IDs are accepted. Group and channel traffic is rejected regardless of this allowlist. The same current allowlist is rechecked for Logs Mini App requests.", Kind: FieldList, Editable: true, Input: FieldInputSpec{ItemShape: "positive-user-id"}, Related: []string{"telegram.enabled", "telegram.topics_enabled", "telegram.logs_mini_app.enabled"}},
	{Key: "telegram.topics_enabled", Label: "Telegram private topics", Section: FieldSectionRuntime, Description: "routes Telegram administration notifications into managed private-chat topics when the bot supports topic mode", Details: "Enabled by default. Topic mode must also be supported for the bot in Telegram. Unsupported bots continue using the General private chat without losing administration or notification delivery.", Kind: FieldBool, Editable: true, Related: []string{"telegram.enabled", "telegram.allowed_user_ids"}},
	{Key: "telegram.logs_mini_app.enabled", Label: "Telegram Logs Mini App", Section: FieldSectionRuntime, Description: "enables the read-only Telegram Logs Mini App ingress", Details: "Enabled by default. Activation requires the external cf-tunnel binary and Telegram authorization. The public URL is runtime-derived and is never persisted.", Kind: FieldBool, Editable: true, Related: []string{"telegram.enabled", "telegram.allowed_user_ids"}},
	{Key: "approval.semantic.enabled", Label: "Semantic approval classification", Section: FieldSectionAccess, Description: "controls optional semantic risk classification for eligible mutations", Details: "Enabled by default. Semantic classification may only preserve or tighten native policy and never grants approval.", Kind: FieldBool, Editable: true},
	{Key: "approval.semantic.provider", Label: "Semantic approval provider", Section: FieldSectionAccess, Description: "selects the provider-neutral risk classifier", Details: "The configured provider must expose the semantic RiskClassifier capability at runtime. Missing capability follows fail_mode.", Kind: FieldString, Editable: true},
	{Key: "approval.semantic.timeout_ms", Label: "Semantic approval timeout", Section: FieldSectionAccess, Description: "sets the bounded classification deadline in milliseconds", Details: "Classification is advisory and locally bounded. Timeout follows fail_mode and never permits execution by itself.", Kind: FieldInt, Editable: true, Input: boundedIntInput(100, 10000)},
	{Key: "approval.semantic.minimum_confidence", Label: "Semantic approval minimum confidence", Section: FieldSectionAccess, Description: "sets the minimum accepted classifier confidence from 0 to 1", Details: "Responses below this threshold are treated as classification failure and follow fail_mode.", Kind: FieldString, Editable: true},
	{Key: "approval.semantic.fail_mode", Label: "Semantic approval failure mode", Section: FieldSectionAccess, Description: "controls how enabled classification failures are handled", Details: "Allowed values are require_approval or deny. An allow failure mode is intentionally unsupported.", Kind: FieldEnum, Options: []string{"require_approval", "deny"}, Values: []FieldValueSpec{{Value: "require_approval", Description: "Escalate classification failure to the canonical human approval workflow."}, {Value: "deny", Description: "Deny the eligible mutation when classification cannot complete safely."}}, Editable: true},
	{Key: "approval.semantic.low_action", Label: "Low-risk semantic action", Section: FieldSectionAccess, Description: "maps low semantic risk to a canonical policy action", Details: "Allow only preserves an existing native allow; it never grants approval or bypasses a deterministic guard.", Kind: FieldEnum, Options: []string{"allow", "require_approval", "deny"}, Values: semanticApprovalActionValues(), Editable: true},
	{Key: "approval.semantic.medium_action", Label: "Medium-risk semantic action", Section: FieldSectionAccess, Description: "maps medium semantic risk to a canonical policy action", Details: "The default escalates medium risk to canonical human approval. Mapping may only preserve or tighten native allow behavior.", Kind: FieldEnum, Options: []string{"allow", "require_approval", "deny"}, Values: semanticApprovalActionValues(), Editable: true},
	{Key: "approval.semantic.high_action", Label: "High-risk semantic action", Section: FieldSectionAccess, Description: "maps high semantic risk to a canonical policy action", Details: "The default escalates high risk to canonical human approval. Mapping may only preserve or tighten native allow behavior.", Kind: FieldEnum, Options: []string{"allow", "require_approval", "deny"}, Values: semanticApprovalActionValues(), Editable: true},
	{Key: "approval.semantic.critical_action", Label: "Critical-risk semantic action", Section: FieldSectionAccess, Description: "maps critical semantic risk to a canonical policy action", Details: "The default denies critical risk. Semantic output never executes a side effect or creates an approval capability directly.", Kind: FieldEnum, Options: []string{"allow", "require_approval", "deny"}, Values: semanticApprovalActionValues(), Editable: true},
	{Key: "explain.mode", Label: "AI explanation mode", Section: FieldSectionRuntime, Description: "controls optional LLM explanations for supported CodeMCP operations", Details: "Off disables explanations. Manual allows supported surfaces to request one. Auto allows eligible lifecycle consumers, such as approval reviews and background-process notifications, to enrich their presentation asynchronously. Enabling manual or auto requires the active LLM provider to be configured and pass an explicit probe before the setting is persisted.", Kind: FieldEnum, Options: []string{"off", "manual", "auto"}, Values: []FieldValueSpec{{Value: "off", Description: "Do not generate LLM explanations."}, {Value: "manual", Description: "Generate explanations only when explicitly requested."}, {Value: "auto", Description: "Allow eligible lifecycle consumers to generate explanations asynchronously."}}, Editable: true},
	{Key: "notifications.approval.enabled", Label: "Approval notifications", Section: FieldSectionRuntime, Description: "controls whether approval lifecycle notifications are delivered to configured providers", Details: "Enabled by default. Review surfaces remain independent of notification delivery; provider availability never consumes or hides canonical approval events.", Kind: FieldBool, Editable: true, Related: []string{"notifications.approval.pending", "notifications.approval.resolved", "notifications.approval.desktop_enabled", "notifications.approval.telegram_enabled"}},
	{Key: "notifications.approval.pending", Label: "Pending approval notifications", Section: FieldSectionRuntime, Description: "controls notification delivery when a request becomes pending", Details: "This policy only controls outbound notification delivery. Pending requests remain visible through every review surface regardless of notification state.", Kind: FieldBool, Editable: true, Related: []string{"notifications.approval.enabled"}},
	{Key: "notifications.approval.resolved", Label: "Resolved approval notifications", Section: FieldSectionRuntime, Description: "controls notification delivery for terminal approval outcomes", Details: "Resolved notifications cover approved, denied, expired, cancelled, and revoked outcomes. They do not alter canonical approval state.", Kind: FieldBool, Editable: true, Related: []string{"notifications.approval.enabled"}},
	{Key: "notifications.approval.desktop_enabled", Label: "Desktop approval notifications", Section: FieldSectionRuntime, Description: "enables native desktop approval notifications when the host supports them", Details: "Linux uses notify-send when available and macOS uses osascript. Unsupported or unavailable native notification facilities are reported as delivery diagnostics and never fail approval state changes.", Kind: FieldBool, Editable: true, Related: []string{"notifications.approval.enabled"}},
	{Key: "notifications.approval.telegram_enabled", Label: "Telegram approval notifications", Section: FieldSectionRuntime, Description: "enables Telegram approval notifications when a Telegram sender is configured", Details: "Enabled by default. The Telegram interface supplies the provider transport once configured; missing transport is unavailable delivery, not an approval failure.", Kind: FieldBool, Editable: true, Related: []string{"notifications.approval.enabled"}},
	{Key: "notifications.completion.enabled", Label: "Completion notifications", Section: FieldSectionRuntime, Description: "controls whether accepted agent completion records are delivered to configured notification providers", Details: "Enabled by default. Completion truth is already durable before notification delivery begins; provider failures never change the accepted completion record.", Kind: FieldBool, Editable: true, Related: []string{"notifications.completion.desktop_enabled", "notifications.completion.telegram_enabled"}},
	{Key: "notifications.completion.desktop_enabled", Label: "Desktop completion notifications", Section: FieldSectionRuntime, Description: "enables native desktop notifications for accepted agent completions", Details: "Notifications include only the bounded completion status, title, and sanitized summary. Unsupported or unavailable desktop facilities are recorded as delivery diagnostics without changing completion truth.", Kind: FieldBool, Editable: true, Related: []string{"notifications.completion.enabled"}},
	{Key: "notifications.completion.telegram_enabled", Label: "Telegram completion notifications", Section: FieldSectionRuntime, Description: "enables Telegram notifications for accepted agent completions when a Telegram sender is configured", Details: "Enabled by default. Missing Telegram transport is treated as unavailable delivery and never changes durable completion truth.", Kind: FieldBool, Editable: true, Related: []string{"notifications.completion.enabled"}},
	{Key: "integrations.ponytail.active", Label: "Ponytail active", Section: FieldSectionIntegrations, Description: "controls whether Ponytail guidance is active by default", Details: "Ponytail biases coding work toward the smallest correct solution: reuse existing code, prefer standard/platform features, avoid speculative abstractions, and minimize unnecessary implementation.", Kind: FieldBool, Editable: true, Related: []string{"integrations.ponytail.mode"}},
	{Key: "integrations.ponytail.mode", Label: "Ponytail mode", Section: FieldSectionIntegrations, Description: "sets the default Ponytail intensity", Details: "This persisted value selects the default runtime intensity when Ponytail is active. Session-only modes such as review/off are not valid persisted values.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Build the requested solution but point out a simpler alternative when useful."}, {Value: "full", Description: "Enforce the reuse/stdlib/native-first ladder and prefer the shortest correct implementation."}, {Value: "ultra", Description: "Apply aggressive YAGNI pressure, favor deletion or minimal implementation, and challenge unnecessary scope."}}, Editable: true, Related: []string{"integrations.ponytail.active"}},
	{Key: "integrations.caveman.active", Label: "Caveman active", Section: FieldSectionIntegrations, Description: "controls whether Caveman response style is active by default", Details: "Caveman compresses assistant prose while preserving technical meaning, exact code, commands, numbers, and safety-critical clarity.", Kind: FieldBool, Editable: true, Related: []string{"integrations.caveman.mode"}},
	{Key: "integrations.caveman.mode", Label: "Caveman mode", Section: FieldSectionIntegrations, Description: "sets the default Caveman response intensity and language register", Details: "The persisted mode controls how aggressively response prose is compressed. The wenyan variants use progressively stronger classical Chinese compression. Session-only aliases such as off or wenyan are not persisted modes.", Kind: FieldEnum, Options: []string{"lite", "full", "ultra", "wenyan-lite", "wenyan-full", "wenyan-ultra"}, Values: []FieldValueSpec{{Value: "lite", Description: "Remove filler and hedging while keeping normal professional sentences."}, {Value: "full", Description: "Use terse fragments where clear and aggressively remove nonessential prose."}, {Value: "ultra", Description: "Maximize compression while preserving unambiguous technical meaning."}, {Value: "wenyan-lite", Description: "Use a semi-classical Chinese register with moderate compression."}, {Value: "wenyan-full", Description: "Use strongly compressed classical Chinese sentence patterns."}, {Value: "wenyan-ultra", Description: "Use extreme classical Chinese abbreviation while retaining meaning."}}, Editable: true, Related: []string{"integrations.caveman.active"}},
	{Key: "integrations.rtk.enabled", Label: "RTK enabled", Section: FieldSectionIntegrations, Description: "controls whether RTK executable resolution is active", Details: "When enabled, CodeMCP resolves RTK in deterministic order: an explicitly configured executable, the system PATH, then a checksum-verified managed asset. Command rewriting is handled separately by the shell integration runtime.", Kind: FieldBool, Editable: true, Related: []string{"integrations.rtk.path"}},
	{Key: "integrations.rtk.path", Label: "RTK executable", Section: FieldSectionIntegrations, Description: "sets an explicit RTK executable path", Details: "Leave empty to resolve RTK from PATH and then the verified managed asset. A configured value must be an absolute path; runtime resolution validates that it is a non-empty executable file before use.", Kind: FieldString, Editable: true, Related: []string{"integrations.rtk.enabled"}},
	{Key: "integrations.codegraph.enabled", Label: "CodeGraph enabled", Section: FieldSectionIntegrations, Description: "controls whether CodeGraph runtime resolution is active", Details: "Enabled by default. CodeMCP resolves an explicitly configured executable, then the system PATH, then a checksum-verified managed CodeGraph asset.", Kind: FieldBool, Editable: true, Related: []string{"integrations.codegraph.path"}},
	{Key: "integrations.codegraph.path", Label: "CodeGraph executable", Section: FieldSectionIntegrations, Description: "sets an explicit CodeGraph executable path", Details: "Leave empty to use system/managed resolution. A configured value must be absolute; execution remains bounded and requires an explicit workspace directory.", Kind: FieldString, Editable: true, Related: []string{"integrations.codegraph.enabled"}},
	{Key: "integrations.browser.enabled", Label: "Browser integration enabled", Section: FieldSectionIntegrations, Description: "controls optional Chrome, Chromium, or Edge capability detection", Details: "Enabled by default. Browser absence is a normal unavailable capability. CodeMCP never downloads or updates a browser and never uses the user's ordinary browser profile.", Kind: FieldBool, Editable: true, Related: []string{"integrations.browser.path"}},
	{Key: "integrations.browser.path", Label: "Browser executable", Section: FieldSectionIntegrations, Description: "sets an explicit Chrome, Chromium, or Edge executable", Details: "Leave empty for platform discovery. An explicit path has strict precedence: if it is invalid or unusable CodeMCP reports that failure and does not silently fall back to another browser.", Kind: FieldString, Editable: true, Related: []string{"integrations.browser.enabled"}},
	{Key: "integrations.typesafe.enabled", Label: "TypeSafe enabled", Section: FieldSectionIntegrations, Description: "controls whether the optional TypeSafe semantic provider may be used", Details: "Enabled by default. Enabling does not contact TypeSafe; remote requests occur only when a semantic consumer or explicit probe uses the configured provider.", Kind: FieldBool, Editable: true, Related: []string{"integrations.typesafe.model", "integrations.typesafe.timeout_ms", "integrations.typesafe.api_key"}},
	{Key: "integrations.typesafe.model", Label: "TypeSafe model", Section: FieldSectionIntegrations, Description: "sets the TypeSafe System One model or alias", Details: "The provider currently documents jev-latest as the stable alias. Versioned model IDs may be used when a consumer needs a pinned calibration target.", Kind: FieldString, Editable: true, Related: []string{"integrations.typesafe.enabled"}},
	{Key: "integrations.typesafe.timeout_ms", Label: "TypeSafe timeout", Section: FieldSectionIntegrations, Description: "sets the local deadline budget in milliseconds for TypeSafe provider operations", Details: "The timeout is locally enforced and remains bounded even if the provider SDK supports a larger/default timeout.", Kind: FieldInt, Editable: true, Input: boundedIntInput(100, 30000), Related: []string{"integrations.typesafe.enabled"}},
	{Key: "tunnel.enabled", Label: "Tunnel", Section: FieldSectionTunnel, Description: "controls whether the OpenAI Secure MCP Tunnel transport is enabled", Details: "Enabled by default. Runtime activation requires both tunnel.id and a configured runtime API key. The tunnel can satisfy the requirement that at least one MCP transport remains enabled when the local MCP HTTP server is disabled.", Kind: FieldBool, Editable: true, Related: []string{"tunnel.id", "tunnel.api_key", "http.mcp.enabled"}},
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
	{Key: "tunnel.control_plane_base_url", Label: "Control-plane URL", Section: FieldSectionTunnel, Description: "overrides the OpenAI tunnel control-plane base URL", Details: "When empty, the tunnel client uses its default control-plane endpoint. A custom value must be an absolute HTTP or HTTPS URL with a host.", Kind: FieldString, Editable: true, Input: FieldInputSpec{Shape: "url"}, Guidance: "Leave empty unless a different control-plane endpoint is explicitly required.", Related: []string{"tunnel.enabled", "tunnel.id"}},
	{Key: "tunnel.organization_id", Label: "Organization ID", Section: FieldSectionTunnel, Description: "sets the OpenAI organization context associated with tunnel runtime operations", Details: "This optional organization identifier is carried in tunnel runtime configuration and is distinct from the verified admin-key organization scope.", Kind: FieldString, Editable: true, Related: []string{"tunnel.admin.organization_id", "tunnel.enabled"}},
}

func boundedIntInput(minimum, maximum int) FieldInputSpec {
	return FieldInputSpec{MinInt: minimum, MaxInt: maximum, HasMinInt: true, HasMaxInt: true}
}

func AcceptedValueHint(spec FieldSpec) string {
	if !spec.Writable && !spec.Editable {
		switch {
		case spec.ValueRole == SettingValueGenerated || spec.Rotatable:
			return "generated · rotate to replace"
		case spec.Derived || spec.ValueRole == SettingValueDerived:
			return "read-only · derived"
		default:
			return "read-only"
		}
	}
	if spec.Secret || spec.Sensitive {
		return "<secret> · protected input"
	}
	var hint string
	switch spec.Kind {
	case FieldBool:
		hint = "true | false"
	case FieldEnum:
		hint = strings.Join(spec.Options, " | ")
	case FieldInt:
		switch {
		case spec.Input.HasMinInt && spec.Input.HasMaxInt:
			hint = fmt.Sprintf("integer %d..%d", spec.Input.MinInt, spec.Input.MaxInt)
		case spec.Input.HasMinInt:
			hint = fmt.Sprintf("integer >= %d", spec.Input.MinInt)
		case spec.Input.HasMaxInt:
			hint = fmt.Sprintf("integer <= %d", spec.Input.MaxInt)
		default:
			hint = "<integer>"
		}
	case FieldList:
		item := strings.TrimSpace(spec.Input.ItemShape)
		if item == "" {
			item = "item"
		}
		hint = "<" + item + ">[, <" + item + ">...]"
	case FieldString:
		shape := strings.TrimSpace(spec.Input.Shape)
		if shape == "" {
			shape = "text"
		}
		hint = "<" + shape + ">"
	case FieldReadOnly:
		hint = "read-only"
	}
	if spec.Selector != nil {
		resource := "resource-id"
		switch {
		case strings.Contains(spec.Key, "providers[<id>]"):
			resource = "provider-id"
		case strings.Contains(spec.Key, "managed[<id>]"):
			resource = "tunnel-id"
		}
		if hint == "" {
			return "<" + resource + ">"
		}
		return "<" + resource + "> → " + hint
	}
	return hint
}

func semanticApprovalActionValues() []FieldValueSpec {
	return []FieldValueSpec{
		{Value: "allow", Description: "Preserve an otherwise allowed native decision; this never grants an approval."},
		{Value: "require_approval", Description: "Escalate the eligible mutation to the canonical human approval workflow."},
		{Value: "deny", Description: "Deny the eligible mutation."},
	}
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
	if spec, ok := FieldByKey(key); ok {
		if err := validateFieldInput(spec, raw); err != nil {
			return err
		}
	}
	switch key {
	case "http.mcp.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.MCP.Enabled = value
	case "http.exposure":
		value, err := ParseExposure(raw)
		if err != nil {
			return err
		}
		cfg.HTTP.Exposure = value
	case "http.exposure.mode":
		mode := ExposureMode(strings.ToLower(strings.TrimSpace(raw)))
		if mode != ExposureNone && mode != ExposureAll && mode != ExposureWildcard && mode != ExposureInterfaces {
			return errors.New("http.exposure.mode must be none, all, 0.0.0.0, or interfaces")
		}
		cfg.HTTP.Exposure.Mode = mode
		cfg.HTTP.Exposure = NormalizeExposure(cfg.HTTP.Exposure)
	case "http.exposure.interfaces":
		interfaces := splitFieldList(raw)
		if len(interfaces) == 0 {
			cfg.HTTP.Exposure = ExposureConfig{Mode: ExposureNone, Interfaces: []string{}}
		} else {
			cfg.HTTP.Exposure = NormalizeExposure(ExposureConfig{Mode: ExposureInterfaces, Interfaces: interfaces})
		}
	case "http.mcp.port":
		value, err := parseIntField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.MCP.Port = value
	case "http.security.allow_insecure":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.Security.AllowInsecure = value
	case "http.security.allow_unauthenticated_loopback":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.Security.AllowUnauthenticatedLoopback = value
	case "http.admin.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.Admin.Enabled = value
	case "http.admin.port":
		value, err := parseIntField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.Admin.Port = value
	case "http.mcp.auth.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.MCP.Auth.Enabled = value
	case "http.mcp.auth.legacy_bearer":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.MCP.Auth.LegacyBearer = value
	case "http.admin.auth.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.HTTP.Admin.Auth.Enabled = value
	case "permissions.allow_dirs":
		cfg.Permissions.AllowDirs = splitFieldList(raw)
	case "permissions.mcp_config_read":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Permissions.MCPConfigRead = value
	case "permissions.mcp_config_write":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Permissions.MCPConfigWrite = value
	case "shell.path":
		cfg.Shell.Path = splitFieldList(raw)
	case "telemetry.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Telemetry.Enabled = value
	case "telegram.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Telegram.Enabled = value
	case "telegram.topics_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Telegram.TopicsEnabled = value
	case "telegram.logs_mini_app.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Telegram.LogsMiniApp.Enabled = value
	case "telegram.allowed_user_ids":
		items := splitFieldList(raw)
		ids := make([]int64, 0, len(items))
		seen := map[int64]struct{}{}
		for _, item := range items {
			id, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
			if err != nil || id <= 0 {
				return errors.New("telegram.allowed_user_ids must contain positive numeric IDs")
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		cfg.Telegram.AllowedUserIDs = ids
	case "approval.semantic.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Approval.Semantic.Enabled = value
	case "approval.semantic.provider":
		value := strings.TrimSpace(raw)
		if value == "" {
			return errors.New("approval.semantic.provider must not be empty")
		}
		cfg.Approval.Semantic.Provider = value
	case "approval.semantic.timeout_ms":
		value, _ := strconv.Atoi(strings.TrimSpace(raw))
		cfg.Approval.Semantic.TimeoutMS = value
	case "approval.semantic.minimum_confidence":
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || value < 0 || value > 1 {
			return errors.New("approval.semantic.minimum_confidence must be between 0 and 1")
		}
		cfg.Approval.Semantic.MinimumConfidence = value
	case "approval.semantic.fail_mode":
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "require_approval" && value != "deny" {
			return errors.New("approval.semantic.fail_mode must be require_approval or deny")
		}
		cfg.Approval.Semantic.FailMode = value
	case "approval.semantic.low_action", "approval.semantic.medium_action", "approval.semantic.high_action", "approval.semantic.critical_action":
		value := strings.ToLower(strings.TrimSpace(raw))
		if value != "allow" && value != "require_approval" && value != "deny" {
			return fmt.Errorf("%s must be allow, require_approval, or deny", key)
		}
		switch key {
		case "approval.semantic.low_action":
			cfg.Approval.Semantic.LowAction = value
		case "approval.semantic.medium_action":
			cfg.Approval.Semantic.MediumAction = value
		case "approval.semantic.high_action":
			cfg.Approval.Semantic.HighAction = value
		case "approval.semantic.critical_action":
			cfg.Approval.Semantic.CriticalAction = value
		}
	case "explain.mode":
		value := ExplainMode(strings.ToLower(strings.TrimSpace(raw)))
		switch value {
		case ExplainOff, ExplainManual, ExplainAuto:
			cfg.Explain.Mode = value
		default:
			return errors.New("explain.mode must be off, manual, or auto")
		}
	case "notifications.approval.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Approval.Enabled = value
	case "notifications.approval.pending":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Approval.Pending = value
	case "notifications.approval.resolved":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Approval.Resolved = value
	case "notifications.approval.desktop_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Approval.DesktopEnabled = value
	case "notifications.approval.telegram_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Approval.TelegramEnabled = value
	case "notifications.completion.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Completion.Enabled = value
	case "notifications.completion.desktop_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Completion.DesktopEnabled = value
	case "notifications.completion.telegram_enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Notifications.Completion.TelegramEnabled = value
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
	case "integrations.browser.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.Browser.Enabled = value
	case "integrations.browser.path":
		cfg.Integrations.Browser.Path = strings.TrimSpace(raw)
	case "integrations.typesafe.enabled":
		value, err := parseBoolField(raw, key)
		if err != nil {
			return err
		}
		cfg.Integrations.TypeSafe.Enabled = value
	case "integrations.typesafe.model":
		value := strings.TrimSpace(raw)
		if value == "" {
			return errors.New("integrations.typesafe.model must not be empty")
		}
		cfg.Integrations.TypeSafe.Model = value
	case "integrations.typesafe.timeout_ms":
		value, _ := strconv.Atoi(strings.TrimSpace(raw))
		cfg.Integrations.TypeSafe.TimeoutMS = value
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
	case "http.mcp.auth.token_hash", "http.admin.auth.token_hash":
		return errors.New("token hashes cannot be set through config; use cm auth <mcp|admin> create")
	default:
		return fmt.Errorf("unsupported config key: %s", key)
	}
	return nil
}

func validateFieldInput(spec FieldSpec, raw string) error {
	if spec.Kind != FieldInt || (!spec.Input.HasMinInt && !spec.Input.HasMaxInt) {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s must be an integer", spec.Key)
	}
	switch {
	case spec.Input.HasMinInt && spec.Input.HasMaxInt && (value < spec.Input.MinInt || value > spec.Input.MaxInt):
		return fmt.Errorf("%s must be between %d and %d", spec.Key, spec.Input.MinInt, spec.Input.MaxInt)
	case spec.Input.HasMinInt && value < spec.Input.MinInt:
		return fmt.Errorf("%s must be at least %d", spec.Key, spec.Input.MinInt)
	case spec.Input.HasMaxInt && value > spec.Input.MaxInt:
		return fmt.Errorf("%s must be at most %d", spec.Key, spec.Input.MaxInt)
	default:
		return nil
	}
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
	case "http.mcp.enabled":
		return strconv.FormatBool(cfg.HTTP.MCP.Enabled), nil
	case "http.exposure":
		exposure := NormalizeExposure(cfg.HTTP.Exposure)
		if exposure.Mode == ExposureInterfaces {
			return strings.Join(exposure.Interfaces, ","), nil
		}
		return string(exposure.Mode), nil
	case "http.exposure.mode":
		return string(NormalizeExposure(cfg.HTTP.Exposure).Mode), nil
	case "http.exposure.interfaces":
		return strings.Join(NormalizeExposure(cfg.HTTP.Exposure).Interfaces, ","), nil
	case "http.mcp.port":
		return strconv.Itoa(cfg.HTTP.MCP.Port), nil
	case "http.security.allow_insecure":
		return strconv.FormatBool(cfg.HTTP.Security.AllowInsecure), nil
	case "http.security.allow_unauthenticated_loopback":
		return strconv.FormatBool(cfg.HTTP.Security.AllowUnauthenticatedLoopback), nil
	case "http.admin.enabled":
		return strconv.FormatBool(cfg.HTTP.Admin.Enabled), nil
	case "http.admin.port":
		return strconv.Itoa(cfg.HTTP.Admin.Port), nil
	case "http.mcp.auth.enabled":
		return strconv.FormatBool(cfg.HTTP.MCP.Auth.Enabled), nil
	case "http.mcp.auth.legacy_bearer":
		return strconv.FormatBool(cfg.HTTP.MCP.Auth.LegacyBearer), nil
	case "http.admin.auth.enabled":
		return strconv.FormatBool(cfg.HTTP.Admin.Auth.Enabled), nil
	case "http.mcp.auth.token_hash":
		return cfg.HTTP.MCP.Auth.TokenHash, nil
	case "http.admin.auth.token_hash":
		return cfg.HTTP.Admin.Auth.TokenHash, nil
	case "permissions.allow_dirs":
		return strings.Join(cfg.Permissions.AllowDirs, ","), nil
	case "permissions.mcp_config_read":
		return strconv.FormatBool(cfg.Permissions.MCPConfigRead), nil
	case "permissions.mcp_config_write":
		return strconv.FormatBool(cfg.Permissions.MCPConfigWrite), nil
	case "shell.path":
		return strings.Join(cfg.Shell.Path, ","), nil
	case "telemetry.enabled":
		return strconv.FormatBool(cfg.Telemetry.Enabled), nil
	case "telegram.enabled":
		return strconv.FormatBool(cfg.Telegram.Enabled), nil
	case "telegram.topics_enabled":
		return strconv.FormatBool(cfg.Telegram.TopicsEnabled), nil
	case "telegram.logs_mini_app.enabled":
		return strconv.FormatBool(cfg.Telegram.LogsMiniApp.Enabled), nil
	case "telegram.allowed_user_ids":
		values := make([]string, len(cfg.Telegram.AllowedUserIDs))
		for index, id := range cfg.Telegram.AllowedUserIDs {
			values[index] = strconv.FormatInt(id, 10)
		}
		return strings.Join(values, "\n"), nil
	case "approval.semantic.enabled":
		return strconv.FormatBool(cfg.Approval.Semantic.Enabled), nil
	case "approval.semantic.provider":
		return cfg.Approval.Semantic.Provider, nil
	case "approval.semantic.timeout_ms":
		return strconv.Itoa(cfg.Approval.Semantic.TimeoutMS), nil
	case "approval.semantic.minimum_confidence":
		return strconv.FormatFloat(cfg.Approval.Semantic.MinimumConfidence, 'f', -1, 64), nil
	case "approval.semantic.fail_mode":
		return cfg.Approval.Semantic.FailMode, nil
	case "approval.semantic.low_action":
		return cfg.Approval.Semantic.LowAction, nil
	case "approval.semantic.medium_action":
		return cfg.Approval.Semantic.MediumAction, nil
	case "approval.semantic.high_action":
		return cfg.Approval.Semantic.HighAction, nil
	case "approval.semantic.critical_action":
		return cfg.Approval.Semantic.CriticalAction, nil
	case "explain.mode":
		return string(cfg.Explain.Mode), nil
	case "notifications.approval.enabled":
		return strconv.FormatBool(cfg.Notifications.Approval.Enabled), nil
	case "notifications.approval.pending":
		return strconv.FormatBool(cfg.Notifications.Approval.Pending), nil
	case "notifications.approval.resolved":
		return strconv.FormatBool(cfg.Notifications.Approval.Resolved), nil
	case "notifications.approval.desktop_enabled":
		return strconv.FormatBool(cfg.Notifications.Approval.DesktopEnabled), nil
	case "notifications.approval.telegram_enabled":
		return strconv.FormatBool(cfg.Notifications.Approval.TelegramEnabled), nil
	case "notifications.completion.enabled":
		return strconv.FormatBool(cfg.Notifications.Completion.Enabled), nil
	case "notifications.completion.desktop_enabled":
		return strconv.FormatBool(cfg.Notifications.Completion.DesktopEnabled), nil
	case "notifications.completion.telegram_enabled":
		return strconv.FormatBool(cfg.Notifications.Completion.TelegramEnabled), nil
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
	case "integrations.browser.enabled":
		return strconv.FormatBool(cfg.Integrations.Browser.Enabled), nil
	case "integrations.browser.path":
		return cfg.Integrations.Browser.Path, nil
	case "integrations.typesafe.enabled":
		return strconv.FormatBool(cfg.Integrations.TypeSafe.Enabled), nil
	case "integrations.typesafe.model":
		return cfg.Integrations.TypeSafe.Model, nil
	case "integrations.typesafe.timeout_ms":
		return strconv.Itoa(cfg.Integrations.TypeSafe.TimeoutMS), nil
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
	setTreeValue(tree, "http.mcp.auth.token_hash", RedactedValue)
	setTreeValue(tree, "http.admin.auth.token_hash", RedactedValue)
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
	key = strings.TrimSpace(key)
	aliases := map[string]string{
		"server.enabled":                        "http.mcp.enabled",
		"server.port":                           "http.mcp.port",
		"server.expose":                         "http.exposure",
		"server.expose.mode":                    "http.exposure.mode",
		"server.expose.interfaces":              "http.exposure.interfaces",
		"server.allow_insecure_http":            "http.security.allow_insecure",
		"server.allow_unauthenticated_loopback": "http.security.allow_unauthenticated_loopback",
		"admin.enabled":                         "http.admin.enabled",
		"admin.port":                            "http.admin.port",
		"auth.mcp_enabled":                      "http.mcp.auth.enabled",
		"auth.mcp_legacy_bearer":                "http.mcp.auth.legacy_bearer",
		"auth.admin_enabled":                    "http.admin.auth.enabled",
		"auth.mcp_token_hash":                   "http.mcp.auth.token_hash",
		"auth.admin_token_hash":                 "http.admin.auth.token_hash",
		"auth.mcp_token":                        "http.mcp.auth.token",
		"auth.admin_token":                      "http.admin.auth.token",
		"auth.mcp_token_configured":             "http.mcp.auth.token_configured",
		"auth.admin_token_configured":           "http.admin.auth.token_configured",
		"approval.explain.mode":                 "explain.mode",
	}
	if canonical, ok := aliases[key]; ok {
		return canonical
	}
	return key
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
