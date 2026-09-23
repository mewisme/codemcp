# Connect ChatGPT with OpenAI Secure MCP Tunnel

OpenAI Secure MCP Tunnel is the default way to connect ChatGPT to `CodeMCP`. The tunnel is established outbound from your machine, so the local MCP runtime does not need a public inbound port.

```text
ChatGPT
   │
   ▼
OpenAI Secure MCP Tunnel
   │ outbound HTTPS
   ▼
cm
   │
   └─ registered local workspaces and tools
```

## What you need

- A running `CodeMCP` installation.
- An OpenAI Platform tunnel (`tunnel_...`).
- A restricted runtime API key with **Tunnels Read + Use**.
- The tunnel associated with the ChatGPT workspace/account that should discover it.
- ChatGPT Developer Mode access for the user creating the custom app.

The runtime API key is only for the tunnel transport. It is not used to call a language model.

## Keep these values separate

| Value | Secret? | Purpose |
| --- | --- | --- |
| Tunnel ID (`tunnel_...`) | No | Selects the OpenAI-hosted tunnel |
| Runtime API key (`sk-...`) | Yes | Lets the local runtime use the tunnel |
| Platform Admin API key | Yes | Administrative tunnel operations; not the normal runtime credential |
| MCP/Admin tokens | Yes | Authenticate direct local/network endpoints when those endpoints are enabled |

For the normal setup, the only OpenAI credential `CodeMCP` needs long-term is a restricted runtime key with **Tunnels Read + Use**.

## 1. Create the tunnel

Open OpenAI Platform tunnel settings:

https://platform.openai.com/settings/organization/tunnels

Create a tunnel and copy its ID:

```text
tunnel_...
```

Associate the tunnel with the ChatGPT workspace that should use it. A tunnel that exists in Platform but is not associated with the target ChatGPT workspace may not appear when creating the custom app.

## 2. Create the runtime API key

Open:

https://platform.openai.com/settings/organization/api-keys

Create a **Restricted** key for the tunnel runtime with:

```text
Tunnels: Read
Tunnels: Use
```

Do not use a Platform Admin API key as the long-lived runtime key.

Tunnel creation/editing is an administrative task and requires **Tunnels Read + Manage** for the operator performing it. That permission does not need to be granted to the daemon's runtime key.

## 3. Initialize and register a workspace

If needed:

```bash
cm init
cm workspace register ~/projects/my-project
```

See [Workspaces](workspaces.md) for how workspace scope works.

## 4. Configure the local tunnel

```bash
cm tunnel configure \
  --enabled \
  --id tunnel_... \
  --api-key 'sk-...'
```

Inspect the result:

```bash
cm tunnel status
```

The runtime key is kept in the selected config root's managed secret store and is not printed by normal status/config output.

## 5. Start the runtime

For normal use:

```bash
cm up
```

Verify:

```bash
cm status
cm tunnel status
```

The tunnel should reach its ready/connected state before ChatGPT scans or invokes tools.

For foreground testing only:

```bash
cm serve
```

See [Runtime and operations](runtime.md) for service behavior, remote Linux usage, and logs.

## 6. Enable ChatGPT Developer Mode

OpenAI controls Developer Mode access separately from Platform tunnel permissions. Availability and the exact settings surface vary by ChatGPT plan/workspace policy, so use the current Help Center article below as the source of truth.

Current settings commonly appear under:

```text
Settings → Apps → Advanced Settings → Developer Mode
```

or from the workspace app-creation flow.

If the UI differs, use OpenAI's current Help Center guidance linked below.

## 7. Create the ChatGPT app

Open **Settings → Apps → Create** (or **Workspace Settings → Apps → Create** when your workspace policy uses the admin surface).

Then:

1. Create a developer-mode custom app.
2. Enter the app metadata you want users to see.
3. Choose **Tunnel** for the connection.
4. Select or enter the same `tunnel_...` used by `CodeMCP`.
5. Configure app-level authentication only if your MCP surface requires it. Do **not** paste the tunnel runtime key into the app's normal bearer-auth field.
6. Run **Scan Tools**.
7. Review the discovered tools and create/enable the app.

## 8. Verify from ChatGPT

Before the first prompt, check locally:

```bash
cm status
cm tunnel status
```

Then try a read-only action in ChatGPT, such as listing registered workspaces or reading runtime/version information.

For live diagnostics while testing:

```bash
cm logs --component TUNNEL -f
```

For full diagnostics:

```bash
cm logs --component TUNNEL --debug -f
```

## Common problems

### The tunnel does not appear in ChatGPT

Check:

1. The tunnel is associated with the target ChatGPT workspace.
2. Your Platform principal has the required tunnel permissions.
3. Your ChatGPT user has Developer Mode access.
4. `cm tunnel status` shows the intended tunnel.
5. The runtime is still running and the tunnel is connected.

Permission/association changes can take time to propagate.

### Tunnel authentication fails

The runtime key likely lacks **Tunnels Read + Use**, belongs to the wrong scope, or is no longer valid. Reconfigure it with `cm tunnel configure` after correcting the Platform permission.

### Scan Tools fails

Keep `CodeMCP` running during discovery and inspect `cm tunnel status` plus tunnel logs. See [Troubleshooting](troubleshooting.md) for more cases.

## What not to do

- Do not expose the local MCP HTTP port publicly just to use Secure MCP Tunnel.
- Do not use an OpenAI Admin API key as the long-lived runtime key.
- Do not paste the tunnel runtime key into the ChatGPT app's normal auth field.
- Do not commit runtime keys, MCP tokens, Admin tokens, or exported secrets.
- Do not grant `Manage` to the runtime key unless the same principal genuinely needs tunnel administration.

## Official OpenAI references

- Secure MCP Tunnel: https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- Developer Mode and MCP apps in ChatGPT: https://help.openai.com/en/articles/12584461
- Platform Tunnels: https://platform.openai.com/settings/organization/tunnels
- Runtime API keys: https://platform.openai.com/settings/organization/api-keys
- Organization roles: https://platform.openai.com/settings/organization/people/roles
