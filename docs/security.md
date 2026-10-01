# Security

`CodeMCP` is designed to give an Agent useful local capabilities without treating the Agent as the owner of the machine's control plane.

The security model has three ideas:

1. **Workspace scope** — project work is constrained to explicitly registered roots and allowed directories.
2. **Control-plane separation** — changing trusted roots, credentials, services, or other protected runtime state is not an ordinary workspace operation.
3. **Private transport by default for ChatGPT** — OpenAI Secure MCP Tunnel avoids public inbound MCP exposure.

This is an application-level boundary. It is **not** a kernel sandbox for deliberately hostile native code running as the same operating-system user.

To report a vulnerability, use [SECURITY.md](../SECURITY.md) and a private GitHub advisory. Do not open a public issue for security bugs.

## Recommended posture

For the normal ChatGPT setup:

- use OpenAI Secure MCP Tunnel;
- keep direct MCP/Admin listeners loopback-only unless another client genuinely needs network access;
- use a restricted tunnel runtime key with **Tunnels Read + Use**;
- register only project roots ChatGPT needs;
- add extra directories narrowly and preferably per-workspace;
- keep endpoint authentication enabled;
- review approval requests before allowing guarded control-plane actions;
- use an OS sandbox, VM/container, or separate user identity for genuinely hostile workloads;
- run `cm config verify --strict` after meaningful access or exposure changes.

## What the boundary protects

Built-in workspace-aware operations enforce canonical roots, reject symlink escapes, and keep workspace-specific state separated. Protected config/runtime state cannot be read or rewritten as ordinary project data through built-in Agent tools.

Guarded control-plane mutations use typed approval semantics rather than granting a general shell bypass.

Managed credentials are redacted from normal configuration/status output and reversible secrets are kept outside ordinary structured configuration.

## What the boundary does not provide

A process deliberately running arbitrary native code as the same OS user may have capabilities beyond an application-level policy. It may access files or networking directly without going through `CodeMCP`, manipulate process state, or attempt to escape process-ancestry assumptions.

If the threat model includes hostile code, use an OS-level sandbox, container/VM, or separate operating-system identity with only the required filesystem access.

## Workspace boundary

The effective filesystem scope for a concrete `ws_*` workspace is:

```text
registered workspace root
+ global permissions.allow_dirs
+ workspace-specific access directories
```

Register a workspace:

```bash
cm workspace register ~/projects/my-project
```

Add a narrow workspace-specific root:

```bash
cm workspace access add ws_... /path/to/build-cache
```

Paths are canonicalized and symlink escapes are rejected.

Built-in filesystem mutations use checkpoint/rewind validation where applicable. If a safe checkpoint cannot be captured completely, the mutation fails before changing the target. Workspace roots and configured allowed roots cannot be removed by checkpointed filesystem operations when doing so would make safe rewind impossible.

Shell-command filesystem changes are not automatically checkpointed.

See [Workspaces](workspaces.md) for the user-facing workspace model.

## MCP session workspace isolation

One MCP session may access multiple registered workspaces. Every workspace-scoped operation still targets a valid concrete `ws_*` ID; the runtime does not infer a hidden current project or silently fall back to another registered workspace.

Workspace-specific filesystem roots, shell cwd, Git/process state, project context, rules, memory, REPL state, checkpoints, and approvals remain isolated by the targeted workspace.

Workspace containers use `wsc_*` IDs and are orchestration-only. Resolving a container or reading its membership grants no filesystem access. Passing a container ID where a concrete workspace is required is rejected rather than selecting or fanning out to a member.

Runtime observability uses safe session metadata/fingerprints rather than exposing raw MCP session IDs in normal activity views.

## Endpoint tenancy

A runtime-level MCP credential authenticates the corresponding endpoint, not an individual workspace. A client that is legitimately authenticated to that runtime can target registered workspaces according to the runtime's tool/session rules.

When two agents must not share the same runtime-level trust domain, use separate runtime instances/config roots or otherwise narrow what is registered. Do not treat workspace IDs themselves as authentication secrets.

## Control-guard approvals and self-grant prevention

MCP-originated shell/process descendants are marked as Agent tool execution context. Read-only/ordinary work remains available; protected control-plane mutations are guarded.

For mutations classified as approvable, the flow is challenge-driven:

```text
Agent action
  → control guard blocks protected mutation
  → approval_required + challenge
  → Agent requests local human approval with a concise title
  → human approves/denies locally
  → Agent retries the exact original action
  → one-shot capability authorizes only that bound retry
```

Approval is bound to the original runtime/session/workspace/tool/action arguments and guard reason. A mismatched retry does not become a general grant. Successful use consumes the approval/capability according to its runtime policy.

The human-readable title is display metadata. It must summarize the action without copying secrets, raw tokens, or unnecessary command arguments.

Some guards are intentionally non-approvable, including attempts to escape workspace/protected paths, tamper with tool-context identity, self-resolve approval requests, or hide a protected mutation inside an unsafe wrapper/compound execution that cannot be bound exactly.

Local operators can review and resolve pending requests through the TUI/Admin surfaces or the `cm request ...` CLI.

Approval Explain may use the configured LLM to generate a fallible explanation for the reviewer. It receives the canonical exact command/action only after secret redaction; the Agent-authored title remains separate display provenance. Explain cannot approve/deny, change deterministic or semantic risk, alter retry binding, create a grant, or extend request lifetime, and the requesting MCP Agent cannot invoke the reviewer-only Explain capability. See [LLM providers](llm.md#approval-explain).

The runtime may also support time-bounded grants for matching command patterns when explicitly approved by the operator. These grants remain runtime-controlled and revocable; they are not an Agent-controlled “allow everything” mode.

### Approval review authorization by interface

All reviewer interfaces call the same approval manager/review operations; authorization differs only at the adapter boundary:

- `cm request ...` and the TUI use the authenticated loopback runtime-control channel owned by the running CodeMCP process. Its credential is protected runtime state, not an Agent capability.
- Browser Admin uses the Admin API. The Admin listener must be enabled; when Admin authentication is enabled, the configured Admin credential is required before approval routes are reached.
- Direct Admin API approval access accepts local loopback review under the local Admin listener policy. Non-loopback review requires Admin authentication to be enabled and the request to authenticate successfully; forwarded-address headers do not create loopback trust.
- MCP/host confirmation UI is not an approval-review surface. An Agent may create a bound CodeMCP approval request, but it cannot approve/deny that request or turn host confirmation into a CodeMCP grant.

These differences change how a reviewer proves authority, not the request state machine, retry binding, similar-command rules, or terminal resolution semantics.

## Protected config/state subtree

The selected config root contains control-plane material such as runtime-control state, configuration, OAuth/upstream state, and managed secret files.

Built-in Agent filesystem/shell policies deny direct access to protected control-plane material through path aliases or symlinks where that would bypass control rules.

This prevents an Agent from simply reading an internal runtime-control credential and using it as a shortcut around the normal control plane.

## Secret storage

Long-lived reversible credentials such as OpenAI tunnel keys, LLM provider API keys, upstream OAuth tokens/client secrets, and sensitive upstream environment/header values are stored through a per-config-root secret store rather than as plaintext structured configuration.

Secret values are encrypted at rest with AES-256-GCM and persisted as versioned JSON envelopes containing a key identifier, nonce, ciphertext, and algorithm metadata. The per-root master key remains separate from those envelopes with restrictive filesystem permissions; the secret-store subtree is excluded from portable configuration exports and normal logs. Structured config keeps only non-secret metadata/configured-state markers.

MCP/Admin endpoint credentials are represented by hashes where appropriate; plaintext endpoint tokens are shown only when created/rotated.

Migrate legacy credentials with:

```bash
cm config migrate
cm config migrate secrets
```

The secret store is not a replacement for OS account security. Keep the config root private to the operating-system user.

## Shell execution boundary

Shell commands inherit the runtime process environment and normal host networking. Configured `shell.path` entries are prepended to `PATH`.

The application still enforces its workspace/control-plane rules around MCP-originated use, but it does not claim to provide portable kernel-level filesystem or network isolation for arbitrary child code.

If stronger isolation is required, provide it externally with an OS sandbox, VM/container, or separate user identity.

## Authentication

MCP and Admin endpoint authentication are distinct policies:

```bash
cm auth status
cm auth mcp create
cm auth admin create
```

Direct authenticated endpoints expect their own credentials. The OpenAI Secure MCP Tunnel runtime API key is separate and must not be confused with an MCP/Admin bearer token.

Protected generic `cm mcp http` uses OAuth as its canonical transport authentication. Static MCP bearer compatibility is a migration path controlled by configuration.

Disabling authentication on an enabled HTTP endpoint requires the corresponding explicit loopback acknowledgement and remains restricted by exposure validation. Prefer authenticated endpoints.

## Network exposure

Loopback-only is the safe default for direct HTTP listeners:

```bash
cm config set http.exposure.mode none
```

Non-loopback direct exposure requires authentication. Because the built-in direct listener is HTTP rather than built-in TLS, broader exposure also requires an explicit insecure-HTTP acknowledgement and should only be used on an appropriately trusted/encrypted network or behind TLS termination.

For ChatGPT, prefer OpenAI Secure MCP Tunnel and avoid public MCP ingress entirely.

Direct exposure modes such as selected interfaces, `all`, or `0.0.0.0` are advanced configuration and should be reviewed with:

```bash
cm config explain http.exposure
cm config verify --strict
```

## OpenAI Secure MCP Tunnel credentials

Keep these roles distinct:

```text
tunnel_id          non-secret tunnel identifier
runtime API key    secret used by the local tunnel runtime
Admin API key      secret for Platform administrative tunnel operations
MCP token          credential for direct MCP endpoint compatibility
Admin token        credential for local Admin endpoint
```

For the normal tunnel runtime, use **Tunnels Read + Use**. Do not use an OpenAI Admin API key as the long-lived daemon key.

See [OpenAI + ChatGPT](openai-chatgpt.md).

## Upstream HTTP outbound policy

HTTP Upstream connections use outbound URL controls intended to reduce SSRF and request-smuggling risk. The current policy includes checks such as:

- rejecting URL userinfo;
- requiring HTTPS for ordinary non-loopback public targets;
- blocking private/link-local/metadata destinations by default;
- re-validating redirects and resolved/dialed addresses;
- rejecting unsafe/hop-by-hop configured headers and CR/LF header injection.

An upstream can explicitly opt into private-network access when that is the intended destination. Keep that exception scoped to the specific upstream.

OAuth discovery/token traffic follows equivalent origin/network safety rules rather than trusting arbitrary metadata to pivot requests into local infrastructure.

## Tunnel network model

OpenAI Secure MCP Tunnel establishes outbound HTTPS from the machine running `CodeMCP` to OpenAI's control plane.

For the default ChatGPT setup:

- no public inbound MCP listener is required;
- the host needs outbound HTTPS to OpenAI;
- the runtime still needs local/private reachability to the tools and workspaces it actually uses.

## Runtime control channel

A running managed/full runtime uses an authenticated loopback control channel for internal operations such as status, reload, shutdown, log handling, approval lifecycle, and verification/consumption of one-shot approved capabilities.

Its credential lives under protected runtime state and is not a user-facing API token. Ephemeral approval challenges/grants/capabilities are invalidated when the owning runtime state is lost or restarted according to their lifecycle.

Remote Admin approval mutation is stricter than ordinary local browsing and requires the configured Admin authentication policy; forwarded-address headers do not turn a remote caller into a trusted loopback caller.

## Runtime journal sanitization

Persistent runtime events are sanitized before writing. The journal is not intended to retain raw authorization credentials, tunnel keys, token hashes, arbitrary full tool arguments containing secrets, or raw file contents.

Operational metadata such as component, event, workspace, tool, source, status, and duration may be retained.

Locate the selected journal with:

```bash
cm logs path
```

Review diagnostic logs before publishing them because project paths or command output may still be sensitive to your environment.

## Config/state isolation

Tests, experiments, and destructive development flows should use an isolated config root:

```bash
CM_CONFIG_DIR=/tmp/cm-test cm status
```

or:

```bash
cm --config-dir /tmp/cm-test ...
```

The repository test/release workflow treats avoiding the real default config root as an invariant.

## Managed services and privilege

On Linux/macOS, machine-level service registration may require `sudo`, but the runtime itself is configured to run as the invoking user rather than root. Linux managed services also apply available service hardening such as `NoNewPrivileges` where supported by the current implementation.

Windows uses a per-user Scheduled Task rather than a LocalSystem service.

See [Runtime and operations](runtime.md#service-scope-by-platform).

## Dangerous combinations

`cm config verify` warns about settings that materially weaken the default posture; `--strict` turns warnings into verification failure.

Examples include:

| Combination | Risk |
| --- | --- |
| Direct listener exposed beyond loopback | Plain HTTP transport unless protected externally; credentials/request data need a trusted encrypted network or TLS terminator |
| Endpoint auth disabled on loopback | Any local process may become a client |
| Broad global `permissions.allow_dirs` | Expands every workspace's filesystem scope |
| Shared runtime credential across different trust domains | Authenticated clients share that runtime-level trust boundary |

Prefer the tunnel-first, narrow-workspace defaults unless a concrete integration requires otherwise.
