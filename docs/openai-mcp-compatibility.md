# OpenAI MCP compatibility baseline

Baseline date: 2026-09-26

This document records the externally documented OpenAI behavior that CodeMCP's
OpenAI MCP profile intentionally supports. It is a compatibility baseline, not
an authority source for CodeMCP security policy. CodeMCP workspace access,
control guards, approvals, config eligibility and secret handling remain
authoritative regardless of host behavior.

## Direct remote MCP

OpenAI documents Streamable HTTP as the production transport for remote MCP
servers. A remote server is identified by its HTTPS MCP endpoint and OpenAI
clients discover tools before invoking them.

CodeMCP compatibility:

- the base profile remains generic MCP;
- the OpenAI profile can be selected for direct Streamable HTTP;
- the OpenAI profile changes presentation/compatibility metadata only and
  cannot change canonical tool schemas, effects, auth requirements or approval
  authority.

Evidence:

- https://developers.openai.com/api/docs/guides/tools-connectors-mcp
- https://developers.openai.com/plugins/concepts/mcp-server

## Tool authentication and linking

OpenAI documents two inputs for ChatGPT linking behavior:

1. OAuth protected-resource metadata published by the MCP server; and
2. per-tool `securitySchemes`, with a compatibility mirror in tool `_meta`
   for clients that still consume it there.

CodeMCP compatibility:

- OAuth remains a generic MCP/core authorization concern;
- the OpenAI profile projects canonical auth requirements into the documented
  tool representation;
- authentication never substitutes for CodeMCP authorization or local
  approval;
- auth challenges preserve the MCP `mcp/www_authenticate` result metadata
  contract where reauthorization/linking is required.

Evidence:

- https://developers.openai.com/plugins/build/auth
- https://developers.openai.com/plugins/reference

## Secure MCP Tunnel

OpenAI documents Secure MCP Tunnel as the connectivity path for private/local
MCP servers. The tunnel client authenticates to the OpenAI control plane and
the tunnel remains a transport path; application-level auth and logging remain
separate.

CodeMCP compatibility:

- Secure MCP Tunnel always consumes the OpenAI MCP profile;
- tunnel connection/session/generation identifiers remain transport lifecycle
  state and are not application authorization identities;
- tunnel control-plane credentials and verification state remain owned by the
  tunnel/config authorities, not by MCP profile projection;
- the same canonical CodeMCP tool runtime, approvals and result semantics are
  used behind direct HTTP and tunnel transports.

Evidence:

- https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- https://developers.openai.com/api/docs/guides/tools-connectors-mcp

## Legacy SSE

Legacy SSE is retained only as an explicit compatibility adapter for older MCP
clients. New profile authority must not depend on it. Current OpenAI guidance
targets Streamable HTTP for remote MCP servers, so OpenAI-specific features are
validated against Streamable HTTP and Secure MCP Tunnel rather than using SSE
as a feature authority.

## Background lifecycle evidence

Evidence date: 2026-09-26

The background lifecycle distinguishes process completion from model
continuation. A completed process can always be inspected or recovered through
the normal CodeMCP process/execution surfaces, but CodeMCP only tells a model to
expect automatic continuation when a concrete client adapter has demonstrated
that capability.

| Client / integration | Profile | Transport | Background lifecycle evidence | Model continuation |
| --- | --- | --- | --- | --- |
| MCP Go SDK v1.7.0 | base | stdio | Official-client initialization/tool calls and fallback guidance are covered by interop tests; explicit recovery is covered by the lifecycle behavior suite. | Not proven |
| MCP Go SDK v1.7.0 | base | Streamable HTTP | Official-client initialization/tool calls and fallback guidance are covered by interop tests; explicit recovery is covered by the lifecycle behavior suite. | Not proven |
| MCP Go SDK v1.7.0 | base | legacy SSE | Compatibility-client initialization/tool calls and fallback guidance are covered by automated tests; explicit recovery remains transport-independent. | Not proven |
| MCP Go SDK v1.7.0 | OpenAI | Streamable HTTP | OpenAI presentation projection plus canonical fallback guidance are covered by official-client interop tests; explicit recovery is covered by the lifecycle behavior suite. | Not proven |
| OpenAI Secure MCP Tunnel | OpenAI | Secure MCP Tunnel | The tunnel/profile bridge and canonical tool/runtime behavior are covered by CodeMCP transport tests; OpenAI documents the tunnel as a normal MCP request path. | Not proven by the CodeMCP test harness |

The provider-neutral continuation coordinator also has a reference capable test
harness that proves event-driven automatic delivery, reconnect fencing,
recovery races and exactly-once model-visible delivery. That harness is not a
claim about a real MCP client. Consequently, MCP request capability projection
keeps model continuation and in-flight steering disabled for the combinations
above, and their instructions tell the model to return control after starting
background work rather than polling.

Evidence:

- https://developers.openai.com/api/docs/guides/tools-connectors-mcp
- https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- https://developers.openai.com/plugins/concepts/mcp-server

## UI boundary

OpenAI documents MCP Apps/UI as optional. CodeMCP does not implement MCP Apps
or OpenAI-specific UI resources in this plan. Tools must remain useful without
custom UI.

Evidence:

- https://developers.openai.com/plugins/build/app-quickstart
- https://developers.openai.com/plugins/build/chatgpt-ui

## Regression requirements

Automated tests must preserve these invariants:

- base stdio and base Streamable HTTP remain usable without OpenAI metadata;
- OpenAI direct Streamable HTTP and Secure MCP Tunnel expose the same canonical
  tool truth with OpenAI presentation metadata layered on top;
- MRTR, extensions and protocol errors remain profile-independent;
- config list/get/set remain secret-safe and agent-eligibility bounded;
- config set requires the exact CodeMCP approval flow and applies/reloads once;
- host confirmation or OpenAI host identity never bypasses CodeMCP approval;
- legacy SSE remains an isolated compatibility adapter.
