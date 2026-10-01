# Troubleshooting

Use this guide for common runtime, service, tunnel, configuration, and connectivity failures.

Start with these three commands:

```bash
cm status
cm tunnel status
cm logs --debug -n 200
```

## MCP session needs to work across multiple workspaces

No workspace-switch command is required. A single MCP session may call workspace-scoped tools against multiple registered projects as long as every call supplies the intended valid `workspace_id`.

If a call targets the wrong project, correct the `workspace_id` on that call rather than changing global session state. The runtime keeps filesystem scope, project context, memory, shell cwd, REPL state, checkpoints, and approvals isolated per workspace. The Activity page shows the target workspace, a short session fingerprint, whether that workspace was a `new` or `existing` access for the session, and the session workspace count; the raw MCP session ID is never exposed.

## Workspace ID changed after a registry v2 upgrade

Registry v2 instance-scoped IDs are migrated to the historical canonical-path ID and retained as aliases, so existing conversations using the old ID continue to resolve to the same workspace. New registrations persist their stable identity in `<workspace>/.cm/workspace.json`; equivalent canonical spellings of the same root therefore reuse one identity. If an ID is genuinely unknown, re-run `cm workspace list` or register the canonical path again; the runtime never falls back to another workspace.

## ChatGPT cannot see the tunnel

Check:

1. The tunnel exists in OpenAI Platform Tunnels.
2. The tunnel is associated with the target ChatGPT workspace, not only a Platform organization.
3. Your Platform principal has **Tunnels Read + Use**.
4. Your ChatGPT user has Developer Mode access enabled.
5. `cm` is running.
6. `cm tunnel status` shows the intended `tunnel_...` ID.
7. Tunnel logs do not show authentication or polling failures.

Useful:

```bash
cm logs --component TUNNEL --debug -n 200
```

OpenAI documents that role/permission changes can take time to propagate, so retry after the new assignment has become active.

See [OpenAI + ChatGPT setup](openai-chatgpt.md).

## Tunnel authentication fails / 403

The runtime API key principal likely does not have the required tunnel permissions or is scoped to the wrong organization/tunnel.

For normal runtime use, grant:

```text
Tunnels Read + Use
```

Tunnel creation/editing requires:

```text
Tunnels Read + Manage
```

Do not replace the runtime key with a Platform Admin API key.

Reconfigure when necessary:

```bash
cm tunnel configure \
  --enabled \
  --id tunnel_... \
  --api-key 'sk-...'
```

Then:

```bash
cm tunnel status
cm logs --component TUNNEL --debug -f
```

## ChatGPT Scan Tools fails

Verify the local runtime is alive while scanning:

```bash
cm status
cm tunnel status
```

Keep it running for both tool discovery and normal calls.

If using foreground mode, do not close the terminal running:

```bash
cm serve
```

For background operation use:

```bash
cm up
```

Then inspect:

```bash
cm logs --debug -f
```

## `cm serve` dies after SSH disconnect

`serve` is a foreground process and is attached to the SSH/session environment.

Use a managed service instead:

```bash
cm up
```

On Linux this is a user systemd service. If the CLI warns that lingering is disabled, the user manager may stop after the final login session ends.

For a machine-level service that starts at boot:

```bash
cm up --system
```

The CLI automatically elevates through `sudo` when needed, using its absolute launcher path so `sudo secure_path` does not need to contain `~/.local/bin`. The MCP process still runs as the invoking user, not root.

## `cm up` says a foreground runtime is already running

`up` intentionally refuses to adopt or kill a foreground `serve` process.

Stop the foreground process normally, then run:

```bash
cm up
```

This prevents service management from unexpectedly taking ownership of a manually started runtime.

## `cm down` cannot remove a system service

On Linux/macOS, system scope and user scope are distinct.

If the service was created with:

```bash
cm up --system
```

remove it with:

```bash
cm down --system
```

Normal `cm down` only manages the normal user-scope service.

## Linux user service warns about lingering

A user-level systemd service can depend on the user manager lifecycle.

`CodeMCP` reports the condition but does not change OS lingering policy automatically.

Options:

- keep the user service and manage lingering yourself according to server policy
- use `cm up --system` for a machine-level systemd unit that starts at boot

## Config changes are not visible in the running server

Config mutations automatically reload a running runtime. If a mutation succeeds but the observed state still looks stale, verify that the command and runtime use the same config root.

Verify:

```bash
cm status
cm config get
```

## A config mutation fails while changing a port

The new listener may be unavailable.

Check:

```bash
cm logs --component SERVER --debug -n 200
```

Listener reload is transactional. A failed new bind restores the previous working listener set, and direct local mutations roll the persisted configuration back.

Choose a free port and retry the mutation:

```bash
cm config set http.mcp.port 41021
```

## Config format mismatch or manual edit failure

Run:

```bash
cm config verify
```

Current CodeMCP machine state is JSON/JSONL only. If verification reports a YAML/TOML structured-state file, treat it as legacy residue and use the explicit migration path for the released installation rather than converting current state in place.

## I accidentally used the real config root in a test

Stop the test process immediately and inspect:

```bash
cm status
cm config path
```

For future test/dev runs, always use:

```bash
cm --config-dir /tmp/cm-test ...
```

or:

```bash
CM_CONFIG_DIR=/tmp/cm-test cm ...
```

Repository tests and release smoke are designed to use explicit non-default roots.

## Workspace path is denied

Check the registered workspace and additional roots:

```bash
cm workspace list
cm workspace show ws_...
cm workspace access list ws_...
cm config get permissions.allow_dirs
```

Register the correct root if needed:

```bash
cm workspace register /absolute/path/to/project
```

Or grant a narrow extra root:

```bash
cm workspace access add ws_... /absolute/path/to/cache
```

Symlink escapes remain denied even when a textual path appears to be inside an allowed directory.

## Workspace folder was renamed or moved

Do not register the destination as a separate workspace if it is the same project. After the directory has already been renamed or moved on disk, rebind the existing workspace:

```bash
cm workspace relocate ws_... /new/path/to/project
```

The stable workspace ID does not change. Move or rename the project directory first so `.cm/` moves with it, then run relocate. The destination must carry the same `.cm/workspace.json` identity; CodeMCP rejects a missing/different identity and also rejects ambiguous copied state where the same identity still exists at both source and destination. Container membership remains attached to the same ID, and access paths nested under the old project root are rebased. Relocate does not rename or move the project directory itself.

If the old root is temporarily missing, `cm workspace list` keeps the workspace registered and reports it as unavailable. Restore the root or relocate it; do not unregister unless you actually want to remove the registry handle. Unregister keeps local `.cm/` state. Use `cm workspace purge ws_... --yes` only when you intentionally want to delete verified local CodeMCP state.

### `.cm` appears in Git status or is already tracked

CodeMCP repairs two local protection layers automatically: `<workspace>/.cm/.gitignore` contains `*`, and Git repositories receive a local `.cm/` rule in their repository metadata `info/exclude`. The project's committed root `.gitignore` is not edited for this purpose. Linked worktrees use the common repository metadata directory, and nested repositories are not modified as a side effect of repairing the parent workspace.

If `.cm` was already tracked before these rules existed, ignore rules cannot remove it from the index. `cm workspace list` and `cm workspace show <workspace_id>` report this condition and the safe manual remediation:

```bash
git rm -r --cached .cm
```

Review the staged deletion before committing. CodeMCP only diagnoses this state; it does not automatically mutate the Git index. On Windows, CodeMCP also requests the native hidden attribute for `.cm/` when supported, but Git protection and workspace correctness do not depend on that attribute.

In `cm tui`, open the workspace detail, press `m` for **Relocate**, choose the new directory, and press `Enter` on the final field to relocate it.

Relocation is intentionally unavailable to MCP tools and agents. It changes the trusted workspace root, so perform it through the local CLI, TUI, or authenticated Admin API instead.

## An Agent action returns `approval_required`

Some control-plane mutations are guarded instead of being executed immediately. When the runtime classifies a direct action as approvable, the Agent receives an `approval_required` challenge and can ask the local operator to review that exact action.

If approved, the Agent must retry the original action exactly. Approval does not create a general shell or CLI bypass.

Other mutations remain hard-denied when they cannot be safely bound, including path/protected-state escape, tool-context tampering, self-approval, or unsafe wrapper/compound execution.

Review pending requests locally with:

```bash
cm request list
cm request view <request_id>
```

See [Security](security.md#control-guard-approvals-and-self-grant-prevention) for the boundary.

## Direct MCP request gets 401/403

Inspect authentication:

```bash
cm auth status
```

Create/rotate an MCP token if needed:

```bash
cm auth mcp create
```

Then send:

```http
Authorization: Bearer <mcp-token>
```

Do not confuse this MCP token with the OpenAI tunnel runtime API key.

## Wildcard exposure is rejected

`0.0.0.0` exposure requires both MCP and Admin authentication with configured tokens.

Inspect:

```bash
cm auth status
```

Create credentials if appropriate:

```bash
cm auth mcp create
cm auth admin create
```

Then configure wildcard exposure only if you genuinely need direct network ingress.

For ChatGPT connectivity, prefer Secure MCP Tunnel and keep the local listener private.

## Logs are too quiet

Replay verbose events:

```bash
cm logs --verbose -n 200
```

Full diagnostics:

```bash
cm logs --debug -n 200
```

Live diagnostics:

```bash
cm logs --debug -f
```

The runtime persists debug-visible structured events even when the service was originally started without `--debug`.

## Logs are too large

The runtime journal rotates automatically.

Locate it:

```bash
cm logs path
```

Clear current and rotated logs intentionally:

```bash
cm logs clear --force
```

When the runtime is alive, clearing is coordinated through the runtime control channel.

## Windows upgrade/install problem while the runtime is active

Current Windows installer layout uses immutable version directories and a stable `current` directory junction so normal upgrades do not overwrite the executable currently held open by the managed runtime.

If upgrading from a legacy installation whose `current` path is still a real directory and Windows cannot migrate it because files are locked:

```powershell
cm down
```

then rerun the installer. Start the managed runtime again afterward:

```powershell
cm up
```

## Upstream refresh fails

Upstream proxy replacement is atomic. A failed discovery/schema refresh should leave the previous proxy catalog active.

Inspect:

```bash
cm status
cm mcp --help
cm logs --debug -n 200
```

Fix the upstream endpoint/auth/discovery issue, then retry the relevant upstream configuration action.

## Still stuck

Collect these without copying secrets:

```bash
cm --version
cm status
cm tunnel status
cm auth status
cm config verify
cm logs --debug -n 200
```

Before sharing logs, review them for project paths, command output, or other environment-specific information you do not want to disclose.
