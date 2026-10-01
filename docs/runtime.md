# Runtime and operations

Use `serve` for a foreground process and `up` for the normal managed background runtime.

| Mode | Command | Best for |
| --- | --- | --- |
| Foreground | `cm serve` | development, one-off testing, direct terminal output |
| Managed | `cm up` | normal daily use, remote servers, restartable background operation |

## Foreground runtime

```bash
cm serve
```

The process stays attached to the current terminal. Closing that terminal or SSH session can stop it.

Useful variants:

```bash
cm serve --verbose
cm serve --debug
```

The default ChatGPT setup still uses OpenAI Secure MCP Tunnel; a foreground runtime starts the configured tunnel along with the local runtime.

## Managed runtime

Start or reconcile the managed service:

```bash
cm up
```

Inspect it:

```bash
cm status
```

Restart it:

```bash
cm restart
```

Stop and remove the managed service definition:

```bash
cm down
```

`down` preserves configuration, workspaces, secrets, and runtime logs. Use `cm uninit` only when you intentionally want to remove the selected local config/state root.

`up` is idempotent: it creates a missing service, starts a stopped service, reconciles a stale definition, or reports an already healthy runtime.

If a foreground `serve` process already owns the selected config root, `up` refuses to silently take it over.

## Service scope by platform

### Linux

```bash
cm up
```

uses a user systemd service.

For a machine-level service that starts with the machine:

```bash
cm up --system
```

The CLI may elevate the service-management operation through `sudo`, but the `CodeMCP` runtime itself is configured to run as the invoking user rather than root.

On remote Linux, use `--system` when a user service would otherwise stop after the final login because user lingering is disabled.

### macOS

`cm up` uses a user LaunchAgent. `cm up --system` uses a system LaunchDaemon while keeping the runtime under the invoking user's identity.

### Windows

`cm up` uses a per-user Task Scheduler task with least privilege. It does not run the runtime as LocalSystem.

## Status

```bash
cm status
```

Use status as the first operational overview. It reports the selected config root, runtime/service state, transport state, tunnel state, relevant endpoints, and registered resource summaries.

For tunnel-specific state:

```bash
cm tunnel status
```

## Logs

Runtime events are persisted under the selected config root and can be replayed or followed live.

History:

```bash
cm logs
cm logs -n 200
cm logs --verbose
cm logs --debug
```

Follow:

```bash
cm logs -f
```

Useful filters:

```bash
cm logs --since 30m
cm logs --level warn
cm logs --component SERVER,TUNNEL
cm logs --workspace ws_...
cm logs --tool run_command --status error
cm logs --grep timeout
```

Locate or clear the journal:

```bash
cm logs path
cm logs clear --force
```

Use `--log-format=json` when consuming event output programmatically. See [CLI reference](cli-reference.md#logs) for the full filter surface.

## Configuration changes

Supported configuration mutations are applied to the running runtime through its local control plane. Network changes such as port or exposure updates are rebound transactionally; if a new listener cannot be opened, the previous working listener set is retained and the local mutation reports failure.

If the runtime is stopped, persisted changes take effect on the next start.

```bash
cm config set http.mcp.port 41021
cm config verify
```

See [Configuration](configuration.md).

## Tunnel lifecycle

The normal managed runtime automatically starts the configured OpenAI Secure MCP Tunnel.

Useful commands:

```bash
cm tunnel status
cm tunnel enable
cm tunnel disable
cm tunnel run
```

`tunnel run` is a foreground tunnel-only operation; normal `serve` / `up` own the usual integrated lifecycle.

See [OpenAI + ChatGPT](openai-chatgpt.md) for setup.

## Updates

Check without changing the installation:

```bash
cm upgrade check
```

Upgrade a managed direct installation:

```bash
cm upgrade
```

Install an exact version, including an intentional downgrade:

```bash
cm upgrade --version vX.Y.Z
```

Keep a running managed runtime on its current in-memory version until a later restart:

```bash
cm upgrade --no-restart
```

Direct managed updates stage the target version, verify release checksums, switch the stable installation target, restart a matching managed runtime when requested, and roll back if the new runtime cannot become ready. A foreground `serve` process is never killed by the updater; restart it manually to load the new binary.

Homebrew and Scoop installations remain owned by their package managers. Development/`go install` binaries do not silently adopt the managed direct-update flow.

## Multiple config roots

Each selected config root is an independent runtime instance:

```bash
cm up
cm --config-dir ~/cm-dev up
cm --config-dir ~/cm-test up
```

Configuration, workspaces, secrets, logs, runtime control state, and service identity remain scoped to the selected root.

## Interactive operation

Open the full-screen Command Center:

```bash
cm tui
```

The TUI can inspect runtime state, logs, requests, workspaces, tunnel state, configuration, and lifecycle actions without replacing the scriptable CLI. See [TUI Command Center](tui.md).

## Troubleshooting

Start with:

```bash
cm status
cm tunnel status
cm logs --debug -n 200
```

Then use [Troubleshooting](troubleshooting.md) for symptom-specific fixes.
