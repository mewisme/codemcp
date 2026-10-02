# Security

CodeMCP is a local application-level trust boundary around AI-accessible development work. It is **not** a kernel sandbox.

## Core boundaries

### Explicit workspaces

Filesystem, shell, Git, process, and context operations target registered workspaces. Sensitive filesystem operations use canonical/root-anchored paths and reject unsafe symlink escapes.

### Canonical control plane

Configuration, credentials, approvals, workspace registration, plans, rules, skills, and other managed resources have explicit application owners. Generic filesystem tools cannot silently become an alternate authority for managed state.

### Approvals

Deterministic local policy runs before optional semantic classification. Approval correlation is bound to the exact pending operation/retry context.

Semantic risk classification can add context but cannot authorize execution when the classifier is unavailable, uncertain, or failed.

### Secrets

Managed credentials live in the selected config root's secret store. Human-facing output shows configured state or masked previews, not raw secrets.

## Network model

The normal ChatGPT path uses OpenAI Secure MCP Tunnel and does not require public inbound MCP exposure. Current OpenAI network/permission details: <https://developers.openai.com/api/docs/guides/secure-mcp-tunnels>.

Direct MCP/Admin HTTP exposure is a separate operator choice. Keep non-loopback exposure authenticated and intentionally scoped.

Remote upstreams and OAuth metadata fetches use outbound URL, redirect, and resolved-address policy. Private/link-local/metadata destinations are rejected unless a feature explicitly opts into the needed private-network access.

## Logs

Runtime/tool logs may contain project paths or command output even when known secrets are redacted. Review logs before publishing them.

## Product telemetry

Telemetry is enabled by default unless disabled by config or `CM_TELEMETRY`:

```bash
cm telemetry status
cm telemetry disable
cm telemetry enable
cm telemetry show
```

Telemetry is bounded product metadata; it does not include prompts, workspace file contents, command arguments/stdout/stderr, tool payloads, credentials, tunnel/Telegram identifiers, approval/process IDs, semantic prompts/responses, or raw logs.

Official release builds receive the product telemetry endpoint at release build time. Ordinary source/developer builds are endpointless by default; a developer must explicitly set `LOCAL_TELEMETRY_ENDPOINT` to opt in.

## Threat-model limit

CodeMCP cannot contain hostile native code already executing with the same OS permissions. Use an OS sandbox, container/VM, restricted account, or separate machine when that isolation is required.

See [SECURITY.md](../SECURITY.md) for vulnerability reporting.
