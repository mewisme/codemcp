# Configuration

## Config root

Default:

```text
~/.cm/
```

Isolated root:

```bash
CM_CONFIG_DIR=/tmp/codemcp-test cm status
cm --config-dir /tmp/codemcp-test status
```

A config root owns structured config, secret-store identity, runtime state, logs, metadata cache, and workspace registry.

## Settings

```bash
cm config ls
cm config get <key>
cm config set <key> <value>
cm config unset <key>
cm config verify
```

`cm config ls` shows canonical accepted-value hints; `--no-accepts` hides that column.

Prefer domain commands such as `cm tunnel`, `cm telegram`, `cm llm`, and `cm integration` when a setting has richer lifecycle or secret semantics.

## Managed secrets

Credentials are not normal plaintext config values. Managed secrets include tunnel credentials, Telegram bot tokens, LLM provider credentials, TypeSafe/SystemOne keys, and supported upstream OAuth/client secrets.

Changing a credential that affects a running runtime is reconciled through its application owner rather than by writing a raw file.

## Authentication and exposure

HTTP MCP auth and local Admin auth are separate. The OpenAI tunnel runtime key is transport authentication and does not replace MCP/tool authorization.

```bash
cm auth --help
cm config ls
```

The default ChatGPT tunnel path does not require public inbound MCP exposure. If you enable direct HTTP, choose bind/exposure settings intentionally and keep authentication enabled unless you have a deliberate trusted-loopback exception.
