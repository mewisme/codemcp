# Product telemetry

CodeMCP can send anonymous, privacy-bounded product telemetry when the build contains a telemetry endpoint and telemetry is effectively enabled. Telemetry delivery is best-effort and never changes command, approval, runtime, or background-work results.

## Controls

Telemetry is enabled by default.

The effective value is resolved in this order:

1. `CM_TELEMETRY`, when it contains a recognized boolean value.
2. Persisted `telemetry.enabled`.
3. The default value, `true`.

Accepted enable values are `1`, `true`, `yes`, and `on`. Accepted disable values are `0`, `false`, `no`, and `off`.

Use:

```bash
cm telemetry status
cm telemetry disable
cm telemetry enable
cm telemetry show
```

`CM_TELEMETRY` is process-local and never changes persisted configuration. The telemetry endpoint is build metadata and is not a user-configurable setting.

## Anonymous identity

When telemetry is enabled and the build contains a valid endpoint, CodeMCP creates a random UUID at:

```text
<CM_CONFIG_DIR or $HOME/.cm>/state/product-telemetry.json
```

The ID is not derived from the machine, user, hostname, workspace, install path, or hardware. Disabling telemetry preserves an existing ID. Re-enabling therefore keeps the same anonymous installation identity. `cm uninit` removes it as part of CodeMCP-owned state.

Status and show operations do not create the identity. Corrupt identity state is treated as absent; a fresh ID is created only on the next eligible enabled operation or bootstrap path.

## Outbound fields

Remote events can contain only:

```text
name
anonymous_id
version
os
arch
interface
command
feature
error_code
duration_ms
success
```

The initial event catalog covers canonical operation completion, runtime start/stop, approval requested/resolved state, and background terminal completion. Error information is reduced to fixed categories rather than raw error text.

CodeMCP does not send command arguments or flag values, stdout/stderr, filesystem paths, workspace/container/session identifiers, prompts, Rules, Skills, Project Context, tool arguments/results, MCP payloads, environment contents, usernames, hostnames, machine or hardware identifiers, credentials, tunnel/OAuth data, Telegram identifiers, approval IDs/payloads, process/execution/task/delivery IDs, upstream request material, semantic-model prompts/responses, or raw local logger/activity/trace events.

## Delivery behavior

Telemetry uses a bounded in-memory queue and short HTTP timeouts. It has no persistent event spool. Failed or ambiguous POSTs are dropped rather than retried automatically. Queue overflow, serialization errors, network failures, server rejection, and shutdown timeouts are telemetry-local failures and do not change functional CodeMCP behavior.

Source and ordinary CI builds have no telemetry endpoint and cannot send remote product telemetry. Release builds receive the endpoint from release build metadata. Tests use endpoint-less execution or local fake ingestion servers rather than the production endpoint.
