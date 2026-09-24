# Internal package map

CodeMCP keeps business ownership below presentation adapters. The intended dependency direction is:

```text
domain/value models
        ^
        |
application services/read models
        ^
        |
runtime/integration/persistence/platform adapters
        ^
        |
CLI / TUI / Admin HTTP / embedded web / protocol entrypoints
```

`app` is the composition root and may wire adapters together. Presentation packages do not own reusable business rules.

## Canonical top-level ownership

| Package | Scope | Canonical responsibility |
| --- | --- | --- |
| `app` | composition | Runtime assembly and interface wiring |
| `application` | application | Shared use cases, mutations, and read models |
| `approval` | domain | Approval state and challenge semantics |
| `auth` | domain | Authentication primitives |
| `capability` | application | Capability metadata/catalog contracts |
| `checkpoint` | history (transitional) | Durable checkpoint history until History ownership cutover |
| `cli` | interface | Top-level process/CLI workflow and command presentation |
| `commandpattern` | domain | Command matching semantics |
| `config` | domain | Configuration model and validation |
| `configbundle` | persistence | Portable JSON configuration envelope persistence |
| `configformat` | persistence | Structured configuration encoding/root paths |
| `controlguard` | domain | Control-plane mutation guard semantics |
| `controlplane` | runtime | Process/control-plane execution policy helpers |
| `git` | platform | Git process adapter |
| `history` | history | Reserved durable-history ownership root |
| `idgen` | domain | Stable/random identifier primitives |
| `install` | platform | Managed installation layout and activation |
| `instance` | persistence | Instance identity persistence |
| `instructioncontext` | application | Canonical instruction/project-context read model |
| `instructionpolicy` | domain/persistence | Instruction discovery and policy |
| `integrations` | integration | First-party Integration ownership for Ponytail, Caveman, RTK, and CodeGraph |
| `interface` | interface | Human/browser presentation adapters |
| `jsruntime` | runtime | Managed JavaScript execution |
| `logger` | runtime | Runtime/log presentation primitives |
| `mcp` | runtime/protocol | MCP protocol server/runtime plumbing |
| `mcpauth` | runtime/protocol | MCP OAuth/auth protocol plumbing |
| `memory` | domain/persistence | Workspace memory model/storage |
| `migration` | persistence | Released-state migration-only readers and transformers |
| `network` | platform | Network interface discovery |
| `oauth` | runtime/protocol | OAuth client/state machinery |
| `oslock` | platform | Cross-process advisory file-lock primitive for runtime ownership and serialized state mutation |
| `outboundpolicy` | domain | Outbound network safety policy |
| `patch` | domain | Deterministic patch primitive |
| `projectcontext` | application | Project Context orchestration/read model |
| `rules` | domain/persistence | Rule discovery/model |
| `runtime` | runtime | Process, activity, control, event, and shell mechanics |
| `secretstore` | persistence | Encrypted secret persistence |
| `service` | platform | OS service lifecycle adapter |
| `skills` | domain/persistence | Skill discovery/model |
| `state` | persistence | Atomic rooted state helpers |
| `systeminfo` | platform | Host/system read model |
| `telemetry` | runtime | Telemetry/observability projection |
| `testutil` | test support | Shared test helpers |
| `tools` | application | Agent tool catalog/runtime orchestration |
| `trace` | runtime | Diagnostic tracing |
| `tunnel` | runtime/integration | OpenAI Secure MCP Tunnel transport/client |
| `update` | platform | Release resolution/download/activation |
| `upstream` | runtime/integration | Upstream client/runtime |
| `version` | domain | Product/build identity |
| `workspace` | domain/persistence | Workspace identity, global registry/path policy, and canonical workspace-local `.cm` state ownership used by memory, checkpoints, shell state, native rules/skills, and workspace Prompt paths |

## Boundary rules

- Lower layers must not import `internal/interface/*`.
- Sibling adapters under `internal/interface/*` must not import each other to reuse business logic.
- `internal/app` is the composition exception: it may wire interface/runtime packages.
- `internal/cli` remains top-level because it owns process entry workflow; its TUI launch is presentation composition, not shared business ownership.
- New first-party optional capabilities belong under `internal/integrations/*`.
- Process/activity/control/event/shell mechanics belong under `internal/runtime/*`.
- Durable append/history ownership belongs under `internal/history/*`.
