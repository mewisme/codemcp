# Integrations

Integrations extend the same CodeMCP runtime; they do not create a parallel authorization model.

## RTK

RTK can rewrite eligible shell commands for compact/structured execution. CodeMCP resolves configured, system, and managed installations deterministically and validates managed assets.

```bash
cm integration rtk --help
```

## CodeGraph

CodeGraph provides repository indexing/search/context and completion-time synchronization. Executable resolution is canonicalized and managed assets are integrity checked.

```bash
cm integration codegraph --help
```

## TypeSafe / SystemOne

TypeSafe supplies SystemOne semantic evaluation and execution-risk classification through one selected provider generation.

Optional semantic enrichment can fail open. Risk-classification failure does **not** authorize execution; eligible execution returns to the normal manual-review/deny boundary.

```bash
cm integration typesafe --help
```

## Browser and ChatGPT Web

CodeMCP can use an installed Chrome, Chromium, or Edge as an isolated browser runtime for the optional ChatGPT Web agent backend. CodeMCP does not download a browser and never reuses the user's ordinary browser profile.

```bash
cm integration browser status
cm integration browser doctor
cm integration chatgpt-web status
cm integration chatgpt-web login
cm integration chatgpt-web doctor
```

Browser discovery is automatic unless an absolute executable is configured through `integrations.browser.path`. The browser profile is CodeMCP-owned and persistent so authentication can survive runtime restarts. Under WSL, CodeMCP prefers a usable native WSL/WSLg browser and otherwise may use an installed Windows browser with its profile stored under Windows LocalAppData.

Managed browser verification and agent tabs are normal graphical browser windows. They can start minimized:

```bash
cm config set integrations.browser.minimized true
```

`integrations.browser.minimized` affects only managed CDP sessions: post-login authentication verification, doctor live verification, and browser-backed agent tabs. Those sessions remain headful and graphical, but CodeMCP starts and enforces the browser window in the minimized state. **Interactive login always opens normally**, even when this setting is enabled. The login command opens a visible CodeMCP-owned browser window without remote debugging or automation attachment so the user can complete ChatGPT/identity-provider login and any browser challenge directly. Close that browser after sign-in; CodeMCP then reopens the same isolated profile under private managed CDP and verifies the authenticated Temporary Chat session.

Managed CDP binds only to loopback. WSL Windows-host automation uses a WSL-local loopback relay to Windows loopback; interactive login does not create that relay. Plain login and managed automation never own the same profile concurrently.

The ChatGPT Web backend uses Temporary Chat for each agent and keeps one browser process with independent tab leases. The first lease reuses the managed bootstrap page; each additional agent gets one sibling page. Releasing one agent does not close its siblings.

ChatGPT Web access is driven through browser UI automation because there is no supported upstream API contract for this workflow. Upstream UI changes, login redirects, or browser challenges can therefore make the backend unavailable until the adapter is updated. CodeMCP fails closed on such conditions: it does not automate CAPTCHA, MFA, passkeys, or other authentication challenges.

The configured OpenAI Secure MCP Tunnel remains the only ChatGPT-to-CodeMCP tool transport. Browser cookies, auth headers, storage, raw session payloads, prompts, and responses are not copied into CodeMCP configuration or logs. After a successful login, the CLI may show only the bounded account name/email returned by the immediate verification result; that identity is not persisted in the auth marker or later status/doctor output.

## LLM providers and Explain

```bash
cm llm status
cm llm provider list
cm llm provider --help
```

Ollama is a core provider. Provider credentials remain in managed secret storage.

Explain is reusable and independent of approval. It receives a redacted command and produces bounded structured explanation content; it does not decide whether a command should be approved or executed.

## Telegram

```bash
cm telegram token set
cm telegram token status
cm telegram --help
```

Bot credentials are managed secrets. Token set/remove reconciles a running runtime; setup rolls back credential changes when later authorization reconciliation fails.

The Logs Mini App is supplemental. Failure to start its quick-tunnel dependency does not disable core Telegram polling.

## Cloudflare quick tunnel

The CF tunnel integration supports temporary supplemental UI flows. It is not OpenAI Secure MCP Tunnel and does not replace CodeMCP's MCP transport authority.
