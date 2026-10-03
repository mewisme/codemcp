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

## Environment overrides

Environment variables are invocation-local unless a command explicitly writes configuration.

- `CM_CONFIG_DIR` selects the config/state root.
- `CM_INSTALL_INTEGRATIONS=0` (also `false`, `no`, or `off`) keeps install/bootstrap detection and reuse of existing integration executables but suppresses downloads for missing managed integration assets.
- `CM_INSTALL_INTEGRATIONS=1` (also `true`, `yes`, or `on`) keeps the default behavior of provisioning missing eligible managed integrations.

`cm install --no-install-integrations` is the CLI equivalent of the false environment value and takes precedence over `CM_INSTALL_INTEGRATIONS`. Invalid environment values are rejected rather than treated as enabled.

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
