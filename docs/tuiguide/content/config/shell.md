# Shell & Execution

Shell configuration changes how `CodeMCP` locates and launches commands; it does not define a separate sandbox or a user-selectable approval mode.

## Executable search path

Configured `shell.path` entries are prepended to the inherited runtime `PATH` for command execution. Paths are validated as configuration values and are shared by foreground/background execution paths where applicable.

## Security boundaries

Keep these concerns separate:

- **Workspace access** defines which filesystem roots a concrete `ws_*` workspace may reach.
- **Control guard and approvals** protect selected control-plane/destructive/host/external actions.
- **OS isolation** is external to `CodeMCP` when you need a kernel-level sandbox.

Changing shell path configuration does not broaden workspace access or bypass approval/control-guard behavior.
