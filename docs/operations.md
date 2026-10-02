# Operations

## Runtime lifecycle

```bash
cm up
cm status
cm restart
cm down
```

Foreground debugging:

```bash
cm serve
```

## Health and logs

```bash
cm status
cm health
cm doctor
cm logs -f
cm tunnel status
```

Logs can contain project paths or command output. Review diagnostics before sharing them.

## Operator interfaces

- **CLI** — scripting and automation; `cm <command> --help` is the command truth.
- **TUI** — `cm tui` for full-screen runtime/workspace/config/log/approval operation.
- **Browser Admin** — embedded local administration using the same application operations.
- **Telegram** — optional notifications and interactive administration.

Telegram token set/remove reconciles a running runtime transactionally. Failed reconciliation restores the prior credential when possible.

## Updates and install ownership

**Managed direct**

```bash
cm upgrade
```

Managed-direct upgrades verify, stage, activate, and preserve rollback behavior.

**Homebrew / Scoop / Debian / RPM**

These remain package-manager-owned. Update with the package manager instead of bypassing ownership with direct self-update.

Package removal does not automatically delete per-user CodeMCP config or registered workspace state.

## Uninitialize

```bash
cm uninit
```

Uninitialization targets CodeMCP user/config state and is different from removing a binary/package.
