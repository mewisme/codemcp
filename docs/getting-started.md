# Getting started

This is the shortest ChatGPT path:

```text
install → init → register workspace → configure tunnel → cm up → connect ChatGPT
```

## Requirements

- Linux, macOS, or Windows on amd64 or arm64.
- A normal user account; do not run the MCP runtime as root.
- For ChatGPT: an OpenAI Secure MCP Tunnel, a runtime key with **Tunnels Read + Use**, and outbound HTTPS to OpenAI.
- No public inbound MCP listener is required for the tunnel path.

For every supported installation method and release filename, see [README → Install](../README.md#install).

## Initialize

```bash
cm init
```

The default config/state root is `~/.cm`. Use `--config-dir` or `CM_CONFIG_DIR` for intentional isolation.

## Register a workspace

```bash
cm workspace register ~/projects/my-project
cm workspace list
```

Registration creates a stable `ws_*` identity. See [Workspaces](workspaces.md).

## Configure OpenAI Secure MCP Tunnel

Create the tunnel in [OpenAI Platform → Tunnels](https://platform.openai.com/settings/organization/tunnels), then configure CodeMCP with its `tunnel_...` ID:

```bash
cm tunnel configure --enabled --id tunnel_...
cm tunnel key set
cm tunnel status
```

Use a runtime key with **Tunnels Read + Use**. Tunnel creation/editing can require **Tunnels Read + Manage**; ChatGPT developer-mode access is a separate workspace permission.

Current OpenAI reference: <https://developers.openai.com/api/docs/guides/secure-mcp-tunnels>.

## Start and verify

```bash
cm up
cm status
cm tunnel status
cm config verify
```

For a temporary foreground run, use `cm serve`. For normal operation, keep the managed runtime.

## Connect ChatGPT

In ChatGPT developer mode, open the developer app/plugin creation flow, choose **Tunnel**, select or enter the same tunnel, scan tools, review the discovered tools, and enable the app/plugin.

The **OpenAI profile** is automatic on this path.

## Generic MCP clients

Local clients can use the Base profile without the OpenAI tunnel:

```bash
cm mcp stdio --workspace ~/projects/my-project
cm mcp http --workspace ws_...
```

See [MCP profiles and transports](mcp.md).
