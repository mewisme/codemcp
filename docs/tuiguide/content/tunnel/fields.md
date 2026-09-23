# Tunnel Editor Fields

## Runtime Tunnel editor

### Enabled

Controls whether the local Secure MCP Tunnel transport is active. If local MCP HTTP is disabled, this field is required to remain enabled so at least one MCP transport stays available. The editor validates that invariant before saving.

### Tunnel ID

Identifier of the tunnel selected for this runtime. It is persisted as the runtime tunnel ID and is required when the tunnel transport is enabled.

### Runtime API key

Sensitive credential used by the runtime to connect to the selected tunnel. The input is password-style. When editing, leaving it blank keeps the currently stored runtime key instead of clearing it.

### Control plane base URL

Optional control-plane endpoint override. Empty/default behavior uses the normal tunnel control-plane endpoint; custom configuration is intended for an explicitly different endpoint.

### Organization ID

Optional organization context associated with runtime tunnel operations. It is not the same as the verified admin-key organization scope.

## Admin Key editor

### OpenAI admin API key (Tunnels Manage)

Sensitive admin credential used for management/control-plane operations. It is distinct from the runtime API key. Submitting the editor verifies the key before storing its usable scope.

### Verification scope

Controls which scope is used when verifying the admin key.

- **Auto (reuse or derive)** reuses/derives scope automatically.
- **Organization** interprets Scope ID as an organization ID.
- **Workspace** interprets Scope ID as a workspace ID.
- **Tenant** interprets Scope ID as a tenant ID.

### Scope ID

Identifier for the selected explicit verification scope. It is ignored when Verification scope is Auto.

## Managed Tunnel — General

### Name

Human-readable managed tunnel name. Required when creating a tunnel.

### Description

Managed tunnel description. Required on create; update allows the existing description to be changed without the create-only requirement.

## Managed Tunnel — Scope

### Organization IDs

Optional multiline list of organization IDs, one per line. Values are normalized before being sent to the management API.

### Workspace IDs

Optional multiline list of workspace IDs, one per line.

### Tenant IDs

Optional multiline list of tenant IDs, one per line.

## Managed Tunnel — Runtime

### Configure cm to use this tunnel

When enabled during create/update, the local runtime is configured to select the managed tunnel after the management operation succeeds.

### Runtime API key

Optional sensitive runtime credential used when configuring the local runtime. Blank reuses the current runtime key.

### Enable tunnel after configure

Controls whether the selected tunnel transport is enabled after configuring the local runtime.

## Use Managed Tunnel editor

### Runtime API key

Same secret-preserving behavior as above: blank reuses the current runtime key.

### Enable tunnel

Controls whether the tunnel is enabled as part of selecting/configuring the managed tunnel for the local runtime.
