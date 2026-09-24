# Configuration

Config is the schema-driven settings surface for the current runtime instance. It groups related fields into operational areas instead of presenting the entire schema as one form.

Use `cm config explain [key]` outside the TUI when you need the exhaustive schema reference for the installed binary.

## Editing fields

Opening an editable field uses the shared full-page editor. `Enter` advances fields and saves when the final visible field completes; multiline values use `Ctrl+Enter`. Failed validation keeps the draft open; managed secret/hash fields never render their underlying secret values.

## Runtime and network

Runtime/network settings control local HTTP transports, ports, exposure, and Admin listener state. The default ChatGPT setup uses OpenAI Secure MCP Tunnel, so direct network exposure is an advanced integration choice rather than a required setup step.

## Access and security

Access/security settings cover endpoint authentication and global extra filesystem roots. Workspace-specific access directories are managed from the Workspaces area.

## Shell and execution

Shell configuration controls values such as additional executable search paths. Workspace containment and control-guard approvals are separate runtime security boundaries rather than configurable shell “approval modes”.

## Tunnel

Tunnel settings describe the local Secure MCP Tunnel transport. Runtime/admin credentials are managed through the Tunnel workflow and remain redacted in ordinary Config views.

## Storage and maintenance

Storage exposes maintenance actions such as verify, migrate, export, and import. Import uses explicit confirmation before replacing persistent state.

## Persisted and live state

Supported configuration mutations are applied to the running runtime automatically. If the runtime is stopped, persisted changes take effect on the next start. Network listener changes are validated/rebound through the runtime configuration path rather than pretending an unavailable listener was applied successfully.
