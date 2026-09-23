# Configuration

`CodeMCP` keeps persistent configuration and runtime state under one selected config root. Use this guide for the configuration model and common operations; use `cm config explain` for the exhaustive schema of the installed version.

## Config root

Default:

```text
~/.cm/
```

Select another root per command:

```bash
cm --config-dir /path/to/instance status
```

or by environment:

```bash
export CM_CONFIG_DIR=/path/to/instance
```

Precedence is:

```text
--config-dir
> CM_CONFIG_DIR
> default user config root
```

A config root owns that instance's configuration, workspaces, secrets, upstream/OAuth state, logs, shell/runtime state, memory, checkpoints, and runtime-control metadata. Use isolated roots for tests or parallel instances.

## Inspect configuration

```bash
cm config get
cm config list
cm config get server
cm config get admin.enabled
```

Structured display is available where supported:

```bash
cm config list --json
cm config list --yaml
cm config list --toml
```

Sensitive fields are redacted.

## Explain the schema

`cm config explain` is the authoritative configuration reference for the installed binary:

```bash
cm config explain
cm config explain server
cm config explain server.expose.mode
cm config explain shell.path
cm config explain shell.path --json
```

A branch explains a subtree; a leaf reports its type, built-in default, editability, valid values, guidance, and related settings where applicable.

The public docs intentionally do not duplicate every schema field, because that inventory would drift from the binary.

## Set values

```bash
cm config set server.enabled false
cm config set server.port 41021
cm config set admin.enabled true
```

Values are parsed according to the schema and validated before persistence. `key=value` syntax is also accepted by the CLI.

At least one MCP transport must remain enabled: direct MCP HTTP (`server.enabled`) or OpenAI Secure MCP Tunnel (`tunnel.enabled`). The default ChatGPT path is the tunnel; direct HTTP is an optional transport for clients that need it.

## Applying changes to a running runtime

Supported local config mutations are applied to the selected running runtime automatically. If the runtime is stopped, the persisted value is used on the next start.

Network-affecting changes such as listener ports or exposure are rebound transactionally. If the new listener cannot be opened, the working listener set is retained and the local mutation reports failure rather than silently leaving runtime and disk in different states.

Verify after meaningful access/network changes:

```bash
cm config verify
cm config verify --strict
```

## Storage format

JSON is the default structured format. YAML and TOML are also supported:

```bash
cm init --json
cm init --yaml
cm init --toml
```

Convert an existing managed structured state tree:

```bash
cm config convert json
cm config convert yaml
cm config convert toml
```

Conversion validates the managed state before activating the new representation.

## Secrets

Long-lived reversible credentials such as tunnel runtime keys, upstream OAuth credentials, and sensitive upstream header/environment values are stored through the selected config root's managed secret store rather than as plaintext values in ordinary structured config.

MCP/Admin endpoint credentials are represented by one-way hashes where appropriate. Normal config/status output does not reveal managed secrets.

Migrate older plaintext credential state:

```bash
cm config migrate
```

Encrypt legacy plaintext secret-store files:

```bash
cm config migrate secrets
```

See [Security](security.md) for the storage and trust model.

## Authentication

MCP and Admin endpoint authentication are separate policies:

```bash
cm auth status
cm auth mcp create
cm auth admin create
cm auth mcp enable
cm auth admin enable
```

Direct authenticated HTTP clients use the credential expected by that endpoint/transport. The OpenAI tunnel runtime API key is different: it authenticates the tunnel client to OpenAI and is not an MCP/Admin bearer token.

Generic protected `cm mcp http` uses OAuth as its canonical transport auth. Legacy static MCP bearer compatibility is controlled by:

```bash
cm config set auth.mcp_legacy_bearer false
```

## Network exposure

The safest direct-listener posture is loopback-only:

```bash
cm config set server.expose none
```

Other supported exposure modes can bind selected interfaces or broader addresses, but non-loopback direct HTTP changes the trust model and requires the appropriate authentication/insecure-HTTP acknowledgement.

For ChatGPT, prefer the Secure MCP Tunnel instead of opening the MCP listener publicly:

```bash
cm tunnel configure --enabled --id tunnel_... --api-key 'sk-...'
```

Read [Security](security.md#network-exposure) before widening exposure.

## Workspace access

Register concrete project roots with:

```bash
cm workspace register ~/projects/my-project
```

Workspace-specific extra roots:

```bash
cm workspace access add ws_... /path/to/cache
```

Global extra roots:

```bash
cm config set permissions.allow_dirs /path/one,/path/two
```

See [Workspaces](workspaces.md) for the canonical `ws_*` / `wsc_*` model and effective scope rules.

## Shell execution

Shell commands inherit the runtime process environment, with configured `shell.path` entries prepended to `PATH`.

`CodeMCP` does not claim to provide a configurable kernel-level process sandbox. Workspace containment, protected control-plane state, and runtime approval/control-guard rules are application-level boundaries. Use an OS sandbox, container/VM, or separate operating-system identity when stronger isolation is required.

See [Security](security.md#shell-execution-boundary).

## Tunnel configuration

Configure the default ChatGPT transport:

```bash
cm tunnel configure \
  --enabled \
  --id tunnel_... \
  --api-key 'sk-...'
```

See [OpenAI + ChatGPT](openai-chatgpt.md) for Platform and ChatGPT setup. Use `cm tunnel --help` for the current local/managed tunnel command surface.

## Upstream MCP configuration

Manage upstream servers with:

```bash
cm upstream --help
cm upstream server --help
```

See [MCP and upstreams](mcp.md).

## Portable backup and transfer

Export the selected portable configuration/state plus managed reversible secrets:

```bash
cm config export
```

Import it on another supported installation:

```bash
cm config import
```

Both default to `codemcp-config.cgm` in the current directory; provide an explicit path when needed.

The portable bundle intentionally excludes transient machine-owned state such as runtime control/PIDs, logs, service-manager definitions, shell session history, checkpoints, and update cache. Import requires the selected runtime to be stopped and protects existing state unless replacement is explicitly requested.

## Remove local config/state

```bash
cm uninit
```

`uninit` removes the selected config/state root. It is different from uninstalling the binary. Stop the matching managed service first when appropriate.
