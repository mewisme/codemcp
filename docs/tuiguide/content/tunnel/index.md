# Tunnel

Tunnel contains two related surfaces: the local runtime tunnel configuration and managed OpenAI Secure MCP Tunnels.

## Runtime tunnel

The Tunnel dashboard shows whether the runtime tunnel is enabled/configured and exposes actions such as configure, enable/disable, foreground run guidance, metadata sync, and admin-key management.

**Configure runtime tunnel** opens a full-page editor. The enabled state uses a Switch. Runtime API key input uses a password-style field; when an existing secret can be reused, that behavior is explained in the placeholder rather than a separate label description.

Save is asynchronous. A backend failure keeps the exact editor draft and feedback. A successful save commits the editor baseline before returning to the dashboard.

## Admin key

The admin key enables OpenAI tunnel-management operations. Verification records positively demonstrated tunnel capabilities without rendering the secret afterward. `Read` permits fetching a known tunnel and using it with a separate runtime credential; `Manage` permits listing, creating, updating, and deleting managed tunnels. The verified capability metadata is stored with the tunnel secret sidecar rather than as user-editable config. Re-verifying refreshes the capability state after OpenAI-side permission changes. Verify and remove remain lifecycle actions; removal requires confirmation.

## Managed tunnels

The Managed Tunnels surface adapts to the verified admin-key capability. Full management access exposes list/create/update/delete plus Use. Read-only access removes mutation actions and limits remote reads to known tunnel IDs; the Web Admin provides a tunnel-ID lookup while TUI/CLI retain read/use actions for known resources. If no capability has been verified, management actions remain unavailable until the key is re-verified. Press `u` on a readable selected TUI row or managed-tunnel detail to open the Use flow.

Editing an existing managed tunnel first fetches current remote metadata. The loading state can be cancelled with `Esc`; a late fetch result after cancellation is ignored. Fetch errors render an explicit wrapped error page instead of a blank editor.

## Configure local runtime from a managed tunnel

**Use managed tunnel** writes the selected managed tunnel into the local runtime configuration. Blank secret/key fields can preserve an existing configured secret when that is supported by the operation.

The CLI equivalent is `cm tunnel use <tunnel_id>` (`select` and `switch` are aliases). `--runtime-api-key` supplies a separate Read + Use credential when no runtime key is already stored, and `--enable` enables the selected tunnel after applying it. The admin key is only used for management discovery and is never substituted for the runtime credential.

## Delete behavior

Deleting a managed tunnel is destructive and uses confirmation rather than a data-entry form. If local runtime configuration points at the tunnel being deleted, the confirmation flow can also handle clearing that local configuration explicitly. The TUI does not hide this consequence inside a generic form toggle.
