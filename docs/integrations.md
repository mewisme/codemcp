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
