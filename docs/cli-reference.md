# CLI reference

CodeMCP installs one executable: `cm`.

Use the built-in help as the authoritative command surface:

```bash
cm --help
cm <command> --help
```

## Useful aliases

Only a small set of high-value aliases is provided:

| Full name | Alias |
| --- | --- |
| `config` | `cfg` |
| `workspace` | `ws` |
| `list` | `ls` |
| `status` | `st` |

Aliases compose with nested commands, for example `cm cfg ls`, `cm ws ls`, `cm upstream server ls`, and `cm tunnel st`.

## Shell completion

Cobra provides command, subcommand, flag, and dynamic argument completion for Bash, Zsh, Fish, and PowerShell:

```bash
# Bash
source <(cm completion bash)

# Zsh
source <(cm completion zsh)

# Fish
cm completion fish | source
```

```powershell
cm completion powershell | Out-String | Invoke-Expression
```

Each generated script registers `cm` only. Dynamic completion includes config keys and typed values, workspace IDs, Upstream IDs, recent runtime session IDs, and directory arguments where appropriate. For example, `cm cfg set per<Tab>` completes `permissions.allow_dirs`, while `cm cfg set http.mcp.auth.enabled <Tab>` offers `true` and `false`.

For source-tree development with direct `go run .` invocations, Bash and Zsh can opt into the Go wrapper hook:

```bash
source <(go run . completion bash --go-run)
# or, in Zsh:
source <(go run . completion zsh --go-run)
```

The hook only redirects completion when the command starts with `go run .`; otherwise it delegates to the previously registered Go completion function when one exists.

## Global flags

| Flag | Purpose |
| --- | --- |
| `--config-dir <path>` | select config/state root; overrides `CM_CONFIG_DIR` |
| `--verbose` | show additional operational context |
| `--debug` | show full diagnostic logging |
| `--log-format text\|json` | choose human CLI-first text or JSON event output |
| `--expose[=<value>]` | one-run network exposure override for commands that start the server |
| `-v`, `--version` | print binary version |

## Command tree

```text
cm
├── auth
│   ├── mcp
│   ├── admin
│   └── status
├── completion
├── config
│   ├── explain
│   ├── export
│   ├── get
│   ├── import
│   ├── list
│   ├── migrate
│   │   └── secrets
│   ├── path
│   ├── set
│   └── verify
├── down
├── init
├── install
├── http
│   ├── admin
│   │   ├── disable
│   │   ├── enable
│   │   └── port
│   ├── exposure
│   │   ├── interface
│   │   │   ├── add
│   │   │   └── remove
│   │   └── mode
│   ├── mcp
│   │   ├── disable
│   │   ├── enable
│   │   └── port
│   └── security
│       ├── insecure
│       │   ├── allow
│       │   └── deny
│       └── loopback
│           └── auth
│               ├── allow
│               └── require
├── logs
│   ├── follow
│   ├── path
│   └── clear
├── llm
│   ├── status
│   ├── use
│   ├── models
│   ├── probe
│   ├── provider
│   └── ollama
├── mcp
│   ├── http
│   ├── stdio
│   └── server      # deprecated compatibility path
├── request
│   ├── approve
│   ├── create
│   │   └── dummy
│   ├── deny
│   ├── explain
│   │   ├── mode
│   │   ├── retry
│   │   └── status
│   ├── grant
│   ├── list
│   └── view
├── restart
├── serve
├── status
├── tui
├── tunnel
│   ├── admin
│   ├── configure
│   ├── create
│   ├── delete
│   ├── disable
│   ├── enable
│   ├── get
│   ├── list
│   ├── run
│   ├── status
│   ├── sync
│   ├── update
│   └── use
├── uninit
├── up
├── upgrade
│   └── check
├── upstream
│   └── server
│       ├── add
│       ├── auth
│       ├── configure
│       ├── disable
│       ├── enable
│       ├── list
│       ├── remove
│       ├── show
│       ├── status
│       └── tools
├── version
└── workspace
    ├── access
    │   ├── add
    │   ├── list
    │   └── remove
    ├── container
    ├── list
    ├── purge
    ├── register
    ├── relocate
    ├── show
    └── unregister
```

Compact root spellings are `cfg` → `config`, `ws` → `workspace`, `tg` → `telegram`, `ups` → `upstream`, and `tel` → `telemetry`. Compatibility spellings such as `req`, `log`, `update`/`upg`, plus parent-scoped forms such as `ls`, `rm`, `st`, and `info`, resolve to the same canonical command and operation as their long form; they do not define separate operations. Alias meaning is scoped by its canonical parent, so the same short token can safely be reused in different command families.

## Installation and updates

Install the current binary into the managed direct-install layout:

```bash
cm install
```

Check or apply updates:

```bash
cm upgrade check
cm upgrade
cm upgrade --version vX.Y.Z
cm upgrade --no-restart
```

`upgrade check` is read-only and always checks the latest release. Built-in mutation is only available for managed direct installs. Homebrew and Scoop installs report the owning package-manager upgrade command; Go/development installs refuse built-in self-update; standalone binaries must run `cm install` first. `upgrade` is the canonical command; `update` and `upg` remain accepted compatibility spellings and do not define separate operations.

Direct updates download the expected platform archive and `codemcp_checksums.txt`, verify SHA-256 before extraction/activation, and switch the stable `current` target transactionally. Exact `--version` allows an intentional downgrade.

When the selected config root has a managed runtime, `cm upgrade` restarts it and waits for full readiness. If the Secure MCP Tunnel is enabled, readiness includes the tunnel reaching its ready state; connecting/reconnecting is not treated as success. Failure restores the previous install target and metadata and restarts the previous runtime. `--no-restart` leaves an existing process on the previous binary; foreground `serve` is also never killed by the updater.

`cm status` never performs a network update check. It may show availability from the fresh install-global cache at `<install-root>/state/update.json`.

## Control approval requests

When an MCP tool hits an approvable control guard, the agent can create a short-lived human request with the `request_control_approval` MCP tool. Local operators inspect and resolve those requests through the running runtime:

```bash
cm request list
cm request view <request_id>
cm request approve <request_id>
cm request deny <request_id>
```

Aliases include `req`, `ls`, `show`/`info`, `accept`/`allow`, and `reject`. Request IDs may be specified in full or by an unambiguous prefix. `approve` and `deny` accept `--reason`; list/view/resolve commands support `--json` where applicable.

Pending requests expire after 60 seconds. Approval does not grant a general CLI bypass: it authorizes one exact retry of the original MCP tool arguments. A mismatched retry is rejected without consuming the valid grant; a successful retry consumes it. `cm request approve/deny` cannot be run by an MCP shell tool to self-approve its own request.

Explain is an optional, non-authoritative shared LLM capability. Its canonical setting is `explain.mode`; `cm request explain mode off|manual|auto` is the approval-review facade for changing that shared mode. Inspect approval Explain availability with `cm request explain status`, and request/retry an approval explanation with `cm request explain <request_id>` / `cm request explain retry <request_id>`. See [LLM providers](llm.md#explain) for provider readiness, background-process enrichment, provenance, and security semantics.

## LLM providers

The `cm llm` tree manages the canonical provider catalog, active selection, model discovery/querying, readiness probes, and managed credentials. Ollama is the permanent default core provider; custom OpenAI- and Anthropic-compatible providers can be added independently. See [LLM providers](llm.md) for complete setup, protected credential input, Ollama local/Cloud behavior, and model-query controls.

## TUI Command Center

`cm tui` is the dedicated full-screen interactive application. Normal CLI commands remain the stable scriptable interface.

```bash
cm tui
cm tui workspace
cm tui workspace ws_...
cm tui upstream github
cm tui logs
cm tui config
```

The TUI requires terminal stdin/stdout. `Ctrl+K` opens Commands for actions, pages, resources, and Guide topics; `Alt+Left` / `Alt+Right` cycle top-level pages, and `Esc` closes the nearest overlay or navigates back.

Use explicit `cm tui` for interactive work and ordinary CLI/JSON output for automation. List commands do not auto-open a TUI and no longer expose per-command `--interactive` / `--no-interactive` flags.

See [TUI Command Center](tui.md) for Commands search, mouse behavior, deep links, forms, confirmations, logs, and scripting guidance. See [Security](security.md#control-guard-approvals-and-self-grant-prevention) for approval challenge binding and one-shot capability semantics.

## Lifecycle

### Initialize

```bash
cm init
```

### Foreground runtime

```bash
cm serve
cm serve --verbose
cm serve --debug
cm serve --expose=eth0
```

### Managed runtime

```bash
cm up
cm status
cm down
```

Linux/macOS system scope:

```bash
cm up --system
cm down --system
```

When invoked from a normal user shell, `--system` automatically re-executes the stable absolute `cm` launcher through `sudo`, so it does not depend on `sudo` including `~/.local/bin` in `secure_path`. For `go run . up|down|restart --system`, the transient Go build is first staged by the invoking user and that staged binary is passed to `sudo`; elevated service management therefore never creates a root-owned `runtime/bin/go-run` cache inside the user's config root. Running the absolute binary under `sudo` directly remains supported for compatibility.

See [Runtime and operations](runtime.md).

## Logs

History:

```bash
cm logs
cm logs -n 200
cm logs --verbose
cm logs --debug
cm logs --log-format=json
cm logs --no-time
```

Runtime log replay is timestamped by default and separated by runtime session. Normal CLI command output remains timestamp-free. Use `--session <run_id-or-prefix>` to isolate one runtime process and `--no-time` to suppress replay timestamps.

Follow:

```bash
cm logs -f
cm logs follow
```

Filters:

```bash
cm logs --since 30m
cm logs --until 2026-08-31T12:00:00+07:00
cm logs --session run_a1b2c3d4e5f6
cm logs --level warn
cm logs --component SERVER,TUNNEL
cm logs --workspace ws_...
cm logs --workspace ~/projects/my-project
cm logs --tool run_command
cm logs --status error
cm logs --source tunnel
cm logs --event 'tool.call.*'
cm logs --grep timeout
```

Journal management:

```bash
cm logs path
cm logs clear --force
```

## Configuration

Inspect persisted values:

```bash
cm config get
cm config list
cm config get http.admin.enabled
cm config list http.admin
```

Explain schema keys and branches:

```bash
cm config explain
cm config explain shell
cm config explain shell.path
cm config explain http.exposure.mode
cm config explain shell.path --json
```

`config explain` is schema-driven and read-only. With no key it walks the full config schema; a branch such as `shell` returns that subtree; a leaf returns its description, details, type, built-in default, editability, valid enum values, guidance, and related keys when available. The reported default is the schema default, not the current persisted value. Legacy aliases are canonicalized before lookup, and sensitive fields expose metadata only, never secret values.

Set:

```bash
cm config set http.mcp.enabled false
cm config set http.mcp.port 41021
cm config set http.admin.port 41022
cm config set http.exposure.mode none
```

At least one MCP transport must remain enabled: `http.mcp.enabled` for direct MCP HTTP or `tunnel.enabled` for OpenAI Secure MCP Tunnel.

The natural scoped facade is `cm http ...` and writes the same canonical settings through the same application setting authority:

```bash
cm http mcp enable
cm http mcp port 41021
cm http admin disable
cm http exposure mode none
cm http exposure interface add eth0
cm http security insecure deny
cm http security loopback auth require
```

Legacy setting spellings under `server.*`, root `admin.*`, and root `auth.*` canonicalize only for compatibility. They are omitted from normal setting inventory/help and must not be used as a second configuration namespace.

Successful config mutations automatically apply to a running process. If the runtime is stopped, they take effect on the next start.

Migrate legacy plaintext credentials to the per-config-root secret file store:

```bash
cm config migrate
```

Migrate legacy per-config-root secret files to versioned JSON envelopes encrypted with AES-256-GCM:

```bash
cm config migrate secrets
```

Verify:

```bash
cm config verify
cm config verify --strict
cm config validate
```

Portable backup/migration:

```bash
cm config export
cm config import
```

Both commands default to `codemcp-config.json` in the current directory. Pass an explicit file only when a custom path/name is needed, for example `cm config export laptop.json` and `cm config import laptop.json`.

`config export` creates a versioned JSON envelope containing portable persistent config/state and an explicit `secret_policy: "excluded"`. Managed secret-store values are not portable through this envelope. `config import` validates and stages the complete envelope before activating it on Linux, macOS, or Windows; a forced import preserves the destination secret store. Existing config/state requires `--force` on import; an existing envelope requires `--force` on export. Import requires the selected runtime to be stopped.

Machine-local filesystem paths are normalized during import. Home-relative paths are mapped to the destination user's home when the corresponding directory exists; unavailable paths and workspaces are skipped. Runtime control state, logs, managed-service environment snapshots, instance identity, shell session state, checkpoints, update cache, and raw secret-store files are intentionally not migrated.

Structured display:

```bash
cm config list --json
```

## Authentication

```bash
cm auth status
cm auth mcp create
cm auth admin create
cm auth mcp enable
cm auth mcp disable
cm auth admin enable
cm auth admin disable
```

`cm mcp stdio` does not use OAuth transport authentication. `cm mcp http` uses OAuth as the canonical protected transport and keeps the existing static MCP bearer only as a compatibility path controlled by `http.mcp.auth.legacy_bearer`.

```bash
cm config set http.mcp.auth.legacy_bearer false
```

Rotating the MCP credential invalidates OAuth codes/tokens issued under the previous credential generation.

Use subcommand help for enable/disable/rotation options exposed by the current binary:

```bash
cm auth mcp --help
cm auth admin --help
```

## Workspaces

Register:

```bash
cm workspace register ~/projects/my-project
```

Inspect:

```bash
cm workspace list
cm workspace show ws_...
```

If the project directory has already been renamed or moved, rebind the existing workspace instead of registering the destination as a new workspace:

```bash
cm workspace relocate ws_... /new/path/to/project
```

`relocate` does not move project files. Move or rename the project first so its local `.cm/` state moves with it, then relocate. The stable `ws_*` ID is preserved. The destination must contain that same `.cm/workspace.json` identity; missing, mismatched, or ambiguously copied local state is rejected. Container membership remains attached to the same ID. Workspace-specific extra roots that were inside the old root are rebased to the new root; unrelated external access roots are left unchanged.

Manage logical workspace containers:

```bash
cm workspace container list
cm workspace container create "Backend projects"
cm workspace container show wsc_...
cm workspace container rename wsc_... "Services"
cm workspace container add wsc_... ws_... [ws_...]
cm workspace container remove wsc_... ws_... [ws_...]
cm workspace container delete wsc_...
```

Container IDs use the `wsc_` prefix. Containers group registered workspaces without merging filesystem scope, project context, shell/REPL state, memory, or checkpoints.

Agent-facing MCP tools expose containers separately:

```text
workspace_container_list()
workspace_container_status(container_id="wsc_...")
workspace_container_context(container_id="wsc_...")
```

`wsc_*` is orchestration-only. Filesystem, Git, shell, memory, rule, checkpoint, and `project_context` calls still require one concrete member `ws_*` as `workspace_id`. Passing an existing container ID as `workspace_id` fails with guidance to resolve the container and choose a member; cm never fans an operation out or silently selects the first member.

When the runtime is already running, every successful CLI workspace-registry mutation synchronously reloads runtime state before returning. This covers workspace register/relocate/unregister, access add/remove, container create/rename/delete, and membership add/remove. The next MCP read therefore sees the change without restarting the runtime or reconnecting the MCP session. If runtime synchronization fails, the CLI reports the failure even though the registry mutation may already have been persisted.

Remove the registry handle without deleting project files or local `.cm/` state:

```bash
cm workspace unregister ws_...
```

Delete workspace-local CodeMCP state explicitly:

```bash
cm workspace purge ws_... --yes
```

`purge` is destructive and requires `--yes`. It unregisters the workspace when needed and deletes only the verified `<workspace>/.cm/` tree; project files outside `.cm/` are unchanged. Registered roots that disappear remain listed as unavailable until restored, relocated, unregistered, or purged.

Additional workspace roots:

```bash
cm workspace access add ws_... /path/to/cache
cm workspace access list ws_...
cm workspace access remove ws_... /path/to/cache
```

## OpenAI Secure MCP Tunnel

Configure:

```bash
cm tunnel configure \
  --enabled \
  --id tunnel_... \
  --api-key 'sk-...'
```

Optional flags:

```text
--control-plane-base-url <url>
--organization-id <org_...>
```

Lifecycle:

```bash
cm tunnel status
cm tunnel enable
cm tunnel disable
cm tunnel run
```

See [OpenAI + ChatGPT setup](openai-chatgpt.md) for Platform/ChatGPT configuration.

## Upstreams

```bash
cm upstream --help
cm upstream server --help
cm upstream server list
cm upstream server show <id>
cm upstream server status <id>
cm upstream server tools <id>
cm upstream server auth --help
```

Use the server subcommands to add, inspect, update, or remove Upstream definitions supported by the current binary.

The former `cm mcp server ...` management path has been removed. Use `cm upstream server ...` for Upstream management.

## Generic MCP clients

Local stdio:

```bash
cm mcp stdio
cm mcp stdio --workspace ~/projects/my-project
```

Cursor project configuration can use `--workspace ${workspaceFolder}` after that project has already been registered with `cm workspace register`.

Standalone Streamable HTTP with legacy SSE fallback:

```bash
cm mcp http
cm mcp http --workspace ws_...
cm mcp http --no-sse
```

The standalone HTTP mode is MCP-only and loopback-only in the current implementation. It does not start the Admin server or OpenAI Secure MCP Tunnel. Its primary endpoint is `/mcp`; legacy SSE compatibility is exposed at `/mcp/sse` unless disabled.

See [MCP and upstreams](mcp.md).

## Status

```bash
cm status
```

Status is the main read-only overview for:

- config root
- runtime state
- foreground/managed service state
- service scope/backend/ID
- runtime session ID
- PID/start information
- MCP HTTP enabled/disabled state and endpoint when enabled
- tunnel enabled/configured/live state
- registered workspaces
- upstream servers
- cached update availability when a fresh install-global cache exists

`status` does not perform a network update check; use `cm upgrade check` for an explicit fresh query.

## Isolated instances

One command:

```bash
cm --config-dir /tmp/cm-test status
```

Environment:

```bash
CM_CONFIG_DIR=/tmp/cm-test cm status
```

Always use an isolated config root when running destructive test/dev flows such as `init`, `uninit`, `config set`, workspace registration, or tunnel configuration.

## CLI logging modes

Default:

```bash
cm status
```

Operational context:

```bash
cm status --verbose
```

Full diagnostics:

```bash
cm status --debug
```

Machine-readable:

```bash
cm status --log-format=json
```

Visibility flags also apply when replaying persistent runtime logs.
