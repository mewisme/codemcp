# Documentation

Start with a task guide, then use the reference docs only when you need exact commands, configuration, protocol, or security details.

## Start here

| I want to… | Read |
| --- | --- |
| Install `CodeMCP` and connect ChatGPT | [Getting started](getting-started.md) |
| Upgrade a released 0.2.24 installation | [Migration from 0.2.24](migration-from-0.2.24.md) |
| Configure the OpenAI tunnel and ChatGPT app | [OpenAI + ChatGPT](openai-chatgpt.md) |
| Understand `ws_*`, workspace scope, and `wsc_*` containers | [Workspaces](workspaces.md) |
| Operate the runtime, services, logs, and updates | [Runtime and operations](runtime.md) |
| Use the full-screen terminal UI | [TUI Command Center](tui.md) |
| Fix a problem | [Troubleshooting](troubleshooting.md) |

## Extend and integrate

| Topic | Guide |
| --- | --- |
| Generic MCP clients, stdio/HTTP transports, upstream servers, OAuth | [MCP and upstreams](mcp.md) |
| LLM providers, models, credentials, Ollama, custom providers, Approval Explain | [LLM providers](llm.md) |
| Authentication, exposure, storage, config roots, import/export | [Configuration](configuration.md) |
| Trust boundaries, approvals, credentials, and network policy | [Security](security.md) |
| Anonymous product telemetry, privacy fields, controls, and identity lifecycle | [Product telemetry](telemetry.md) |

## Reference

The runtime itself is the authoritative source for the live command and configuration surface:

```bash
cm --help
cm <command> --help
cm config explain [key]
```

Use [CLI reference](cli-reference.md) for the curated command map and useful combinations. Use [Configuration](configuration.md) for configuration concepts; `cm config explain` provides the exhaustive schema inventory for the installed version.

## Develop and contribute

- [Development](development.md) — source builds, checks, CI, release workflow
- [Contributing](../CONTRIBUTING.md) — contribution and PR expectations
- [Security policy](../SECURITY.md) — private vulnerability reporting
- [Code of Conduct](../CODE_OF_CONDUCT.md)

## Embedded TUI help

The contextual guides under [`tuiguide/`](tuiguide/) are embedded into `cm tui`. They explain the page or editor a user is currently operating and intentionally do not duplicate the full public documentation.
