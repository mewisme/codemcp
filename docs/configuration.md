# Configuration

`CodeMCP` keeps persistent configuration and runtime state under one selected config root. Use this guide for the configuration model and common operations; use `cm config why` for the exhaustive schema of the installed version.

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
cm config get http
cm config get http.admin.enabled
```

Structured display is available where supported:

```bash
cm config list --json
```

Sensitive fields are redacted.

## Explain the schema

`cm config why` is the authoritative configuration reference for the installed binary:

```bash
cm config why
cm config why http
cm config why http.exposure.mode
cm config why shell.path
cm config why shell.path --json
```

A branch explains a subtree; a leaf reports its type, built-in default, editability, valid values, guidance, and related settings where applicable.

The public docs intentionally do not duplicate every schema field, because that inventory would drift from the binary.

## Canonical local HTTP hierarchy

Local HTTP configuration has one canonical root:

```text
http
├── exposure
│   ├── mode
│   └── interfaces
├── security
│   ├── allow_insecure
│   └── allow_unauthenticated_loopback
├── mcp
│   ├── enabled
│   ├── port
│   └── auth
│       ├── enabled
│       ├── legacy_bearer
│       └── token_hash        # internal verifier metadata
└── admin
    ├── enabled
    ├── port
    └── auth
        ├── enabled
        └── token_hash        # internal verifier metadata
```

Use `http.exposure.*` and `http.security.*` for policy shared by both local HTTP endpoints. Endpoint-local state belongs under `http.mcp.*` or `http.admin.*`.

The scoped CLI facade follows the same hierarchy:

```bash
cm http mcp enable
cm http mcp port 41021
cm http admin disable
cm http exposure mode none
cm http exposure interface add eth0
cm http security insecure deny
cm http security loopback auth require
```

`cm auth ...` remains the dedicated credential lifecycle facade. It operates on `http.mcp.auth.*` and `http.admin.auth.*`; it is not a separate configuration authority.

Legacy persisted roots named `server`, `admin`, or `auth` are migration inputs only. Loading a legacy-only document migrates them into `http` and rewrites the document canonically. A document containing both `http` and any legacy HTTP root is rejected as ambiguous instead of merging two authorities. Unknown fields nested under a legacy HTTP root are also rejected rather than being silently dropped during rewrite.

## Set values

```bash
cm config set http.mcp.enabled false
cm config set http.mcp.port 41021
cm config set http.admin.enabled true
```

Values are parsed according to the schema and validated before persistence. `key=value` syntax is also accepted by the CLI.

At least one MCP transport must remain usable: direct MCP HTTP (`http.mcp.enabled`) or a configured OpenAI Secure MCP Tunnel (`tunnel.enabled` plus its runtime prerequisites). Fresh configuration records several optional capabilities as enabled intent by default; missing third-party credentials or external resources leave those capabilities degraded/unavailable instead of making the whole runtime invalid. Explicit `false` values remain durable operator opt-outs across reload, import, and migration.

## Applying changes to a running runtime

Supported local config mutations are applied to the selected running runtime automatically. If the runtime is stopped, the persisted value is used on the next start.

Network-affecting changes such as listener ports or exposure are rebound transactionally. If the new listener cannot be opened, the working listener set is retained and the local mutation reports failure rather than silently leaving runtime and disk in different states.

Verify after meaningful access/network changes:

```bash
cm config verify
cm config verify --strict
```

## Storage format

CodeMCP-owned machine configuration and structured state use JSON. Append-only machine event/history streams use JSONL. Current runtime persistence does not expose alternate YAML/TOML formats or a format-conversion workflow.

```bash
cm init
```

Released legacy formats are migration inputs only; they are not current CodeMCP persistence formats.

## Secrets

Long-lived reversible credentials such as tunnel runtime keys, upstream OAuth credentials, and sensitive upstream header/environment values are stored through the selected config root's managed secret store rather than as plaintext values in ordinary structured config. Current file-backed secrets are versioned JSON envelopes containing only encryption metadata, nonce, and ciphertext; plaintext credentials are not persisted in those envelopes.

Current MCP/Admin endpoint plaintext credentials are kept in the managed secret store, while one-way token hashes remain in configuration as runtime verifier metadata. Normal config/status output uses the canonical masked preview when the secret is recoverable; verifier-only legacy credentials use an explicit legacy masked placeholder and are never silently rotated.

Migrate older plaintext credential state:

```bash
cm config migrate
```

Migrate legacy secret-store files to encrypted JSON envelopes:

```bash
cm config migrate secrets
```

See [Security](security.md) for the storage and trust model. LLM provider credentials use the same managed-secret authority; see [LLM providers](llm.md) for active-provider aliases and provider-scoped settings.

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
cm config set http.mcp.auth.legacy_bearer false
```

## Network exposure

The safest direct-listener posture is loopback-only:

```bash
cm config set http.exposure.mode none
```

Other supported exposure modes can bind selected interfaces or broader addresses, but non-loopback direct HTTP changes the trust model and requires the appropriate authentication/insecure-HTTP acknowledgement.

For ChatGPT, prefer the Secure MCP Tunnel instead of opening the MCP listener publicly:

```bash
cm tunnel configure --enabled --id tunnel_...
cm tunnel key set
# automation: cm tunnel key set --from-env OPENAI_TUNNEL_API_KEY
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
cm tunnel configure --enabled --id tunnel_...
cm tunnel key set
```

See [OpenAI + ChatGPT](openai-chatgpt.md) for Platform and ChatGPT setup. Use `cm tunnel --help` for the current local/managed tunnel command surface.

## Upstream configuration

Manage upstream servers with:

```bash
cm upstream --help
cm upstream server --help
```

See [MCP and upstreams](mcp.md).

## Portable backup and transfer

Export the selected portable non-secret configuration/state:

```bash
cm config export
```

Import it on another supported installation:

```bash
cm config import
```

Both default to `codemcp-config.json` in the current directory; provide an explicit path when needed.

The versioned JSON envelope declares `secret_policy: "excluded"`: managed secret-store values are never exported or imported. A forced import preserves the target secret store. The envelope also excludes transient machine-owned state such as runtime control/PIDs, logs, service-manager definitions, shell session history, checkpoints, and update cache. Import validates the staged configuration before replacing the selected root, requires the selected runtime to be stopped, and protects existing state unless replacement is explicitly requested.

## Remove local config/state

```bash
cm uninit
```

`uninit` removes the selected config/state root. It is different from uninstalling the binary. Stop the matching managed service first when appropriate.
