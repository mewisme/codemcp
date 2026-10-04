# CodeMCP documentation

A secure, workspace-bound MCP bridge connecting ChatGPT, Claude, and other AI agents to your machine.

Start with [Getting started](getting-started.md). Use the other guides when you need to change a boundary, transport, integration, or operator workflow.

| Task | Guide |
| --- | --- |
| Install, initialize, register a project, and connect ChatGPT | [Getting started](getting-started.md) |
| Choose an MCP profile or transport; configure upstream MCP servers | [MCP profiles and transports](mcp.md) |
| Understand registered workspaces and additional roots | [Workspaces](workspaces.md) |
| Manage workspace/global Agent Skills and GitHub-backed skills | [Skills](skills.md) |
| Run the service, inspect logs, update, and use operator UIs | [Operations](operations.md) |
| Work with config roots, settings, auth, and managed secrets | [Configuration](configuration.md) |
| Configure RTK, CodeGraph, TypeSafe/SystemOne, LLM providers, or Telegram | [Integrations](integrations.md) |
| Review approvals, containment, network policy, telemetry, and privacy | [Security](security.md) |
| Diagnose common failures | [Troubleshooting](troubleshooting.md) |
| Build and verify the repository | [Development](development.md) |

## MCP profiles

CodeMCP has one canonical runtime authority and multiple client projections:

1. **OpenAI profile** — automatic on OpenAI Secure MCP Tunnel; direct HTTP can opt in with `--profile openai`.
2. **Base profile** — generic MCP projection and default for direct stdio/HTTP.

Profiles change presentation and compatibility metadata, not tool effects, workspace scope, authentication requirements, control guards, or approval authority.
