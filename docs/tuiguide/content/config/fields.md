# Configuration Fields

The Config TUI is schema-driven. Each field below is the same key exposed by the application's configuration field registry. Editable fields use the field type declared by the schema; managed credential/scope fields are read-only.

## Runtime & Network

### `server.enabled` — MCP HTTP server

Boolean. Controls whether the local MCP HTTP transport is enabled. Disabling it removes local HTTP MCP connectivity. At least one MCP transport must remain enabled, so the Secure MCP Tunnel must be enabled before this can be disabled by itself. Related: `server.port`, `server.expose.mode`, `auth.mcp_enabled`, `tunnel.enabled`.

### `server.expose.mode` — Exposure

Enum controlling which local network addresses expose HTTP servers.

- `none`: loopback only.
- `all`: loopback plus every eligible address discovered on all interfaces.
- `0.0.0.0`: one IPv4 wildcard listener exposing eligible IPv4 addresses.
- `interfaces`: loopback plus addresses from `server.expose.interfaces`.

Non-loopback exposure also requires `server.allow_insecure_http=true` and valid authentication for each enabled HTTP endpoint.

### `server.expose.interfaces` — Exposure interfaces

List of network interface names used when exposure mode is `interfaces`. Enter one value per line in the TUI. Each name must resolve to an available interface with at least one eligible IP address at runtime. Duplicates are normalized away. Set `server.expose.mode=interfaces` before relying on this list.

### `server.port` — MCP HTTP port

Integer TCP port for the MCP HTTP server. Valid range is `1-65535`. When both MCP and admin HTTP servers are enabled, their ports must differ.

### `server.allow_insecure_http` — Allow insecure HTTP

Boolean opt-in allowing authenticated plain HTTP endpoints beyond loopback. This does not disable authentication requirements. Prefer the Secure MCP Tunnel or a TLS reverse proxy when possible.

### `admin.enabled` — Admin server

Boolean controlling the admin HTTP server. When enabled it uses `admin.port` and the same network exposure policy. If admin authentication is enabled, a configured admin credential is required.

### `admin.port` — Admin port

Integer TCP port for the admin HTTP server. Valid range is `1-65535` while the server is enabled, and it must differ from `server.port` when both HTTP servers are enabled.

## Access & Security

### `auth.mcp_enabled` — MCP authentication

Boolean token-authentication switch for the MCP HTTP endpoint. Non-loopback HTTP exposure requires MCP authentication with a configured credential.

### `auth.admin_enabled` — Admin authentication

Boolean token-authentication switch for the admin HTTP endpoint. Non-loopback exposure with the admin endpoint enabled requires admin authentication and a configured credential.

### `auth.mcp_token_hash` — MCP credential

Read-only, sensitive managed credential hash. The raw token is never exposed through config views. Manage it through the MCP authentication/token workflow rather than Config field editing.

### `auth.admin_token_hash` — Admin credential

Read-only, sensitive managed credential hash for admin HTTP authentication. Manage it through the admin authentication/token workflow.

### `permissions.allow_dirs` — Allowed directories

List of global filesystem roots that registered workspaces may access in addition to workspace-local/per-workspace allowed directories. Paths must be absolute and are normalized.

## Shell & Execution

### `shell.path` — Executable search paths

List of additional executable directories prepended to the inherited runtime `PATH`. Paths must be absolute. Foreground and background shell execution use the same resolved path list.

Shell commands otherwise inherit the runtime process environment. The application still enforces workspace mutation containment, protected control-plane state, and risk-based approval for destructive, host, or external mutations. Strong OS-level process isolation should be provided externally when required.

## Integrations

### `integrations.ponytail.active` — Ponytail active

Boolean controlling whether Ponytail guidance is active by default.

### `integrations.ponytail.mode` — Ponytail mode

Enum default intensity: `lite`, `full`, or `ultra`. Lite builds the requested solution but may point out simpler alternatives; full enforces reuse/stdlib/native-first and shortest-correct implementation; ultra applies aggressive YAGNI pressure and challenges unnecessary scope.

### `integrations.caveman.active` — Caveman active

Boolean controlling whether compressed Caveman response style is active by default.

### `integrations.caveman.mode` — Caveman mode

Enum persisted response intensity: `lite`, `full`, `ultra`, `wenyan-lite`, `wenyan-full`, `wenyan-ultra`. The wenyan variants progressively increase classical-Chinese compression. Session-only aliases such as `off` or `wenyan` are not persisted values.

### `integrations.rtk.enabled` — RTK enabled

Boolean controlling RTK executable resolution. When enabled, CodeMCP resolves an explicitly configured executable first, then the system `PATH`, then a checksum-verified managed asset.

### `integrations.rtk.path` — RTK executable

Optional absolute path to an RTK executable. Leave empty to use system/managed resolution. Managed RTK assets live under the CodeMCP config root and are verified before they can become an executable source.

## Tunnel

### `tunnel.enabled` — Tunnel

Boolean controlling the OpenAI Secure MCP Tunnel transport. Enabling requires both `tunnel.id` and a configured runtime API key. It can satisfy the requirement that at least one MCP transport remains enabled when local MCP HTTP is disabled.

### `tunnel.id` — Tunnel ID

String identifier for the Secure MCP Tunnel used by this runtime. Required while tunnel transport is enabled.

### `tunnel.api_key` — Runtime API key

Read-only sensitive managed credential. The raw key is redacted from Config. Manage it from the Tunnel page.

### `tunnel.admin_key` — Admin key

Read-only sensitive credential used for control-plane management such as listing, creating, updating, and deleting managed tunnels. It is separate from the runtime API key and is managed from the Tunnel page.

### `tunnel.admin_organization_id` — Admin organization scope

Read-only verified organization scope produced by admin-key verification.

### `tunnel.admin_workspace_id` — Admin workspace scope

Read-only verified workspace scope produced by admin-key verification.

### `tunnel.admin_tenant_id` — Admin tenant scope

Read-only verified tenant scope produced by admin-key verification.

### `tunnel.control_plane_base_url` — Control-plane URL

Optional string overriding the tunnel control-plane base URL. Empty uses the default endpoint. A custom value must be an absolute HTTP/HTTPS URL with a host.

### `tunnel.organization_id` — Organization ID

Optional OpenAI organization context associated with runtime tunnel operations. This is distinct from the verified admin-key organization scope.
