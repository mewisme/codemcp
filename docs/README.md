# Documentation

Start with the task you need to complete. The runtime remains the authoritative reference for the installed command and configuration surface.

## Start and operate

| I want to… | Read |
| --- | --- |
| Install CodeMCP and make the first useful connection | [Getting started](getting-started.md) |
| Connect ChatGPT through OpenAI Secure MCP Tunnel | [OpenAI + ChatGPT](openai-chatgpt.md) |
| Register, move, group, or recover workspaces | [Workspaces](workspaces.md) |
| Operate services, updates, logs, background work, and recovery | [Runtime and operations](runtime.md) |
| Use the interactive terminal interface | [TUI Command Center](tui.md) |
| Diagnose a failure | [Troubleshooting](troubleshooting.md) |
| Upgrade an existing 0.2.24 installation safely | [Migration from 0.2.24](migration-from-0.2.24.md) |

## Configure and integrate

| Topic | Guide |
| --- | --- |
| Config roots, authentication, HTTP exposure, import/export, and settings | [Configuration](configuration.md) |
| LLM providers, model discovery, protected credentials, Explain, and TypeSafe semantics | [LLM providers](llm.md) |
| Generic MCP clients, Plan Mode, Upstreams, OAuth, profiles, Skills, and background behavior | [MCP and upstreams](mcp.md) |
| Trust boundaries, approvals, secrets, network policy, telemetry, and privacy | [Security](security.md) |

## Reference

Use the installed binary for exact command and setting lookup:

```bash
cm --help
cm <command> --help
cm config why
cm config why <key>
```

The [CLI reference](cli-reference.md) is the curated command map. [Configuration](configuration.md) explains configuration concepts; `cm config why` is the exhaustive schema reference for the installed version.

## Develop and contribute

- [Development](development.md) — source builds, tests, CI, and release workflow
- [Contributing](../CONTRIBUTING.md) — contribution and PR expectations
- [Security policy](../SECURITY.md) — private vulnerability reporting
- [Code of Conduct](../CODE_OF_CONDUCT.md)
