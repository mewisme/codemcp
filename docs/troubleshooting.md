# Troubleshooting

Start here:

```bash
cm status
cm health
cm doctor
cm config verify
cm tunnel status
cm logs -f
```

## Tunnel enabled but not connected

Verify the tunnel ID, runtime key, **Tunnels Read + Use**, OpenAI organization/workspace associations, and outbound HTTPS. OpenAI reference: <https://developers.openai.com/api/docs/guides/secure-mcp-tunnels>.

## ChatGPT cannot see the tunnel

Platform tunnel permissions and ChatGPT developer-mode workspace access are separate. Ensure the tunnel is associated with the target ChatGPT workspace, not only a Platform organization.

## Workspace path rejected

```bash
cm workspace list
cm workspace show ws_...
```

Register the project explicitly instead of widening access unnecessarily.

## Telegram token change failed

Token changes reconcile the running runtime. On failure, CodeMCP attempts to restore the prior credential and reports rollback/reconciliation status.

```bash
cm telegram token status
cm status
cm logs -f
```

## Generic MCP HTTP fails

```bash
cm mcp http --help
```

Base is the direct-transport default. Use `--profile openai` only when the client needs the OpenAI projection. Protected HTTP still requires configured MCP authentication.

## Clean source checkout does not build

```bash
make bootstrap
make build
```

The Go binary embeds Browser Admin and Telegram Mini App assets. Canonical `make check`, `make test`, and `make test-race` build frontend assets first.

## Package-managed install does not self-upgrade

Homebrew, Scoop, Debian, and RPM remain package-manager-owned. Use that package manager's update flow. `cm upgrade` is for managed-direct ownership.
