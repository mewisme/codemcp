# Runtime & System

Runtime is the operational status/control page for the local `CodeMCP` process and managed installation.

## Status

The page aggregates runtime/service state, MCP HTTP transport, authentication state, managed installation/update information, and build/version information. Refresh reloads this operational snapshot.

## Service lifecycle

Commands can start, stop, or restart the supported user/system managed service scope. System-level actions are hidden where the platform does not support them. Stopping/removing the service preserves configuration and logs unless a separate destructive operation explicitly states otherwise.

Foreground actions show the command that should be run after leaving the TUI rather than trying to replace the current alternate-screen process with a long-running foreground server.

## Authentication

Runtime exposes MCP/admin authentication enable/disable and token rotation actions. Rotated plaintext tokens are intentionally treated as one-time sensitive output. Persisted hashes/secrets are not displayed later in Config or Runtime details.

## MCP HTTP transport

The direct MCP HTTP listener can be enabled or disabled when configuration constraints allow it. At least one MCP transport must remain enabled: direct MCP HTTP or OpenAI Secure MCP Tunnel. The normal ChatGPT setup can therefore run tunnel-only with the direct listener disabled.

## Install and update editors

Install and Apply Upgrade use routed full-page editors rather than confirmation fields embedded inside forms. `Enter` advances the form and starts the requested operation when the final visible field completes. Progress remains an operation overlay so the editor does not disappear while long-running work is active.

Backend failure returns to the editor and preserves its draft. Success commits the editor baseline before navigating away.

Upgrade checks use verified release metadata. Release downloads/checksums/signatures are validated by the update subsystem before activation.

## Configuration lifecycle

Configuration initialization/uninitialization actions that must run outside the current TUI are presented as explicit external command guidance rather than hidden shell execution.
