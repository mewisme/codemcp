# Embedded TUI Guide

These Markdown files are contextual help for the `cm tui` Command Center and are embedded directly into the binary. They should explain the current page/editor without duplicating the full public documentation.

| Topic | Guide |
| --- | --- |
| Getting started and navigation | [Getting Started](content/getting-started.md) |
| Editors and forms | [Editors & Forms](content/editors/index.md) |
| Workspaces, containers, and Project Context | [Workspaces](content/workspaces/index.md) |
| Upstreams and OAuth | [Upstreams](content/mcp/index.md) |
| OpenAI Secure MCP Tunnel | [Tunnel](content/tunnel/index.md) |
| Requests and approvals | [Requests & Approvals](content/requests/index.md) |
| Runtime, command execution, and tool-call logs | [Logs](content/logs/index.md) |
| Configuration | [Configuration](content/config/index.md) |
| Global Context, rules, and sources | [Instruction](content/instruction/index.md) |
| Runtime/service/auth/install/update operations | [Runtime & System](content/runtime/index.md) |

The canonical guide tree lives under `content/`. A leaf is a Markdown file; a branch is a directory with `index.md`. Inside the TUI, use `Ctrl+K` and search for **Guide**, or deep-link with `cm tui guide <topic...>`.

Only the selected document is rendered. Keep implementation details in development/code documentation and keep broad product concepts in the public docs under `docs/`.
