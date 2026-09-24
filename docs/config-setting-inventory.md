# CodeMCP setting inventory

CodeMCP has one metadata vocabulary for values that behave as settings. `config.Settings()` is the canonical inventory used to describe identity, type, ownership, read/write behavior, secret handling, scoped CLI ownership, and dynamic resource selectors.

This inventory describes the configuration domains implemented by the current product. A domain that adds a new persisted setting or managed credential must add its metadata at the same time; configuration state must not exist outside the registry by convention alone.

## Persisted global settings

The normal configuration document contains these setting families:

| Domain | Canonical settings | Application owner |
| --- | --- | --- |
| Server | `server.enabled`, `server.expose.mode`, `server.expose.interfaces`, `server.port`, `server.allow_insecure_http`, `server.allow_unauthenticated_loopback` | config |
| Admin | `admin.enabled`, `admin.port` | config |
| Authentication | `auth.mcp_enabled`, `auth.mcp_legacy_bearer`, `auth.admin_enabled` | auth/config |
| Permissions | `permissions.allow_dirs` | config |
| Shell | `shell.path` | config |
| Ponytail | `integrations.ponytail.active`, `integrations.ponytail.mode` | config |
| Caveman | `integrations.caveman.active`, `integrations.caveman.mode` | config |
| RTK | `integrations.rtk.enabled`, `integrations.rtk.path` | config |
| CodeGraph | `integrations.codegraph.enabled`, `integrations.codegraph.path` | config |
| Secure MCP Tunnel runtime | `tunnel.enabled`, `tunnel.id`, `tunnel.control_plane_base_url`, `tunnel.organization_id` | tunnel runtime |

Editable normal config values are readable, writable, and resettable to their default through the canonical setting operation layer. Metadata records an existing scoped CLI facade where one exists; otherwise it records an explicit current config-only exemption rather than inventing a second command path.

`auth.mcp_token_hash` and `auth.admin_token_hash` are persisted implementation fields, not user-facing setting identities. They are internal-only metadata owned by authentication credential storage.

The verified tunnel fields `tunnel.admin_organization_id`, `tunnel.admin_workspace_id`, and `tunnel.admin_tenant_id` are readable derived metadata. They are populated by admin-key verification and are not directly writable settings.

## Managed credentials

Raw credentials are never normal readable setting values.

| Canonical identity | Read state | Mutation behavior | Normal presentation |
| --- | --- | --- | --- |
| `auth.mcp_token` | `auth.mcp_token_configured` | rotate with `cm auth mcp create` | configured state only |
| `auth.admin_token` | `auth.admin_token_configured` | rotate with `cm auth admin create` | configured state only |
| `tunnel.api_key` | `tunnel.api_key_configured` | set/clear through tunnel runtime configuration | bounded masked preview |
| `tunnel.admin_key` | `tunnel.admin_key_configured` | set, clear, and verify through `cm tunnel admin key ...` | bounded masked preview |

Authentication tokens are hash-backed. The generated plaintext is returned only by the explicit create/rotate operation and cannot be reconstructed from persisted state, so generic reads expose only configured state.

Tunnel runtime/admin credentials are recoverable secret-store values, but normal setting presentation is restricted by metadata to a bounded masked preview. Structured or subtree reads must use the public configured-state identity when a raw secret result is not explicitly requested by an authorized secret operation.

Secret-store account names, encrypted payloads, file markers, storage paths, and authentication hashes are storage implementation details and are not setting keys.

## Upstream resource settings

Upstream identity is selected with a bracketed resource selector such as `upstream.servers[docs.v2].enabled`. The resource ID is percent-decoded from the bracket segment and must resolve through the Upstream inventory before a setting operation may run.

The canonical scalar/list/enum resource settings are:

- `enabled`, `name`, `transport`;
- `command`, `args`, `cwd`;
- `url`, `bearer_token_env_var`;
- `auth.type`, `auth.scope`;
- `tool_prefix`, `expose`, `tools`, `disabled_tools`;
- `idle_timeout_sec`, `allow_private_network`.

Their canonical application owner is the Upstream server domain, and the existing scoped facade is `cm upstream server configure`.

`env` and `headers` are map-valued resource configuration, not scalar/list/enum settings. They remain owned by the Upstream resource operation. Sensitive entries are redacted by the Upstream boundary and are not flattened into arbitrary universal keys. The resource `id` is selector identity, not a mutable setting.

OAuth access/refresh/client credentials are protocol authorization state managed by `cm upstream server auth login|logout|status`; they are not raw configuration settings and are never surfaced as universal secret values.

## Managed OpenAI tunnel settings

Remote managed tunnels are selected with keys such as `tunnel.managed[tun_demo].description`. The selector is resolved through the managed-tunnel inventory rather than an arbitrary map or file path.

The remote mutable setting identities are:

- `name`, `description`;
- `tenant_ids`, `workspace_ids`, `organization_ids`.

Their canonical application owner is managed OpenAI tunnel administration and the existing scoped facade is `cm tunnel update`.

Creating, deleting, selecting, or fetching a managed tunnel is resource lifecycle/CRUD, not a setting. Those operations therefore do not receive setting keys. The local selected-tunnel runtime fields remain the scalar `tunnel.*` settings listed above.

## Selector and presentation contract

Dynamic selectors use `domain.collection[<id>].field`. Resource IDs are RFC 3986 percent-decoded; empty IDs, bracket characters, and `/` are rejected before domain lookup. Completion and mutation must enumerate/validate IDs through the owning resource inventory rather than treating the selector as a filesystem or generic map path.

Readable subtree listings omit internal-only and write-only entries. A write-only managed credential can still be addressed by exact canonical identity for an authorized mutation, while its normal read surface uses the corresponding configured-state key.

There is no generic `mcp.*` profile setting namespace. MCP remains a protocol/transport term; product configuration belongs to its concrete domain such as authentication, Upstream, or Secure MCP Tunnel.
