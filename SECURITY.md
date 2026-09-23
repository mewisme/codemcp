# Security Policy

## Reporting a vulnerability

Do **not** open a public GitHub issue for security vulnerabilities.

Report privately via GitHub Security Advisories:

https://github.com/mewisme/codemcp/security/advisories/new

Please include:

- Affected version (`cgm version`) and platform (OS/arch)
- Description of the issue and impact
- Steps to reproduce or a proof of concept when possible
- Any known mitigations

We will acknowledge reports as soon as practical and coordinate a fix and disclosure timeline. Please give us a reasonable window before public disclosure.

## Scope

In scope (examples):

- Authentication / authorization bypass on MCP or Admin endpoints
- Workspace path escape, symlink escape, or unintended filesystem access
- Privilege escalation via control-plane / approval flows
- Secret leakage (tokens, tunnel keys, OAuth credentials) through logs, exports, or APIs
- Installer or self-update integrity failures (checksum / signature bypass)
- Remote code execution reachable through the intended product surface

Out of scope (examples):

- Issues that require already having local OS user privileges equivalent to the runtime user, unless they expand the product trust boundary
- Social engineering, phishing, or physical access
- Denial of service from exhausting local machine resources without a distinct product bug
- Vulnerabilities only in third-party dependencies with no practical impact on this project (prefer upstream reporting; we still welcome heads-up)

## Product security model

Built-in trust boundaries (workspaces, approvals, auth, exposure modes) are documented in [docs/security.md](docs/security.md). That guide describes intended behavior; this file is only for vulnerability disclosure.
