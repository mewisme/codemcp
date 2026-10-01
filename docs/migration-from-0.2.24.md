# Migrating from 0.2.24

The released 0.2.24 binary predates the current stable, versionless release-artifact contract. CodeMCP does **not** publish a compatibility bridge artifact for that old binary's updater.

If you are starting directly from a released 0.2.24 installation, first reacquire a current `cm` using the installation owner you intend to keep: the shell/PowerShell bootstrap or Windows setup for managed direct installs, Homebrew/Scoop for those package-manager installs, a native Linux package, or a tagged release asset. Do not rely on the 0.2.24 executable to discover the new artifact names.

Once a current `cm` is running, the existing state migration handles the released 0.2.24 installation during `cm install` or `cm upgrade`. The migration detects the released state first, stages and validates the transformed state outside the active CodeMCP root, activates the canonical state transactionally, and only then retires verified historical runtime identities.

The current global state root is `~/.cm`. Registered workspace project files remain user-owned; migration only publishes CodeMCP-owned workspace-local state after ownership and conflict checks pass. Unavailable registered workspaces remain registered and reportable without writing into inaccessible project roots.

## Executable names

Released executable aliases are different from CLI command aliases.

- The historical executable names `chatgpt-mcp` and `cgm` are retired executable aliases. Fresh CodeMCP releases install only the canonical `cm` executable (`cm.exe` on Windows).
- A verified historical executable or service identity may be retired after the canonical CodeMCP runtime is activated and ready. Ambiguous, package-manager-owned, or otherwise unverified files are left untouched.
- CLI command aliases remain normal command spellings inside `cm`. For example, `cm upgrade` is canonical while `cm update` and `cm upg` remain accepted command aliases. Their presence does not recreate an operating-system executable alias.

## Upgrade behavior

For a managed direct installation, `cm upgrade` checks CodeMCP release metadata, verifies the mandatory SHA-256 checksum, stages the new binary, and uses the managed install transaction. Homebrew and Scoop installations remain owned by their package managers.

If a released 0.2.24 state root is detected, migration runs before the new runtime state becomes active. Critical corrupt state fails closed. Optional corrupt UI/log history may be skipped with a report. Runtime-control, PID, lock, update-cache, and other process state are regenerated instead of migrated.

The migration preserves representable authentication and Upstream OAuth semantics, including legacy MCP bearer compatibility settings. It does not fabricate plaintext credentials from one-way hashes.

## Rollback state

Migration keeps its rollback source and transaction journal after successful activation. Successful migration does not by itself delete the durable released state used for recovery. Historical runtime-control artifacts may be retired only after canonical runtime ownership and readiness are verified.

After migration, use:

```bash
cm status
cm config verify
cm upgrade check
```

If the installation is Homebrew-, Scoop-, Debian-, or RPM-owned, keep using that package-manager ownership path for later upgrades.
