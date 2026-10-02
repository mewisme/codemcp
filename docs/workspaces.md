# Workspaces

A registered workspace is CodeMCP's primary filesystem authority boundary.

## Register and inspect

```bash
cm workspace register ~/projects/my-project
cm workspace list
cm workspace show ws_...
```

Registration records a stable `ws_*` identity. Workspace-scoped tools target that identity rather than treating arbitrary paths as ambient authority.

## Containment

CodeMCP canonicalizes workspace roots and uses root-anchored filesystem operations for sensitive reads/writes. Unsafe symlink replacement and traversal outside an authorized root are rejected.

A workspace is an application boundary, not an OS sandbox. Native code already running as the same OS user retains that user's permissions.

## Additional roots

Add extra allowed directories only when the project needs them. Prefer the smallest stable directory boundary rather than broad parents such as your home directory.

```bash
cm permissions --help
cm workspace show ws_...
```

## Workspace-local state

Workspace-owned state lives under the workspace's `.cm/` tree. Identity, memory, rules, skills, plans, checkpoints, and typed state have explicit owners; transient/derived state is kept separate.

## Relocation and duplicates

When CodeMCP detects relocation or duplicate local state, it validates identity and durable-state compatibility before selecting or merging state. Unknown or conflicting state fails closed.

```bash
cm workspace --help
cm doctor
```

Workspace containers group registered workspaces for orchestration; a container is not itself a filesystem root.
