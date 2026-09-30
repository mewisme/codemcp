# LLM providers

CodeMCP has one canonical LLM provider catalog shared by CLI, TUI, Browser Admin, and Telegram. Operator interfaces may present different controls, but provider identity, configuration, credentials, model discovery, readiness, and active-provider selection come from the same application-owned state.

LLM inference is an optional consumer capability. It is not an authorization, approval, or command-risk authority.

## Provider lifecycle

A catalog always has exactly one active provider. Provider-scoped configuration does not implicitly make that provider active; selection is a separate operation.

One core provider always exists and is selected by default:

| Provider | Fresh default | Protocol | Authentication | Discovery |
| --- | --- | --- | --- | --- |
| Ollama | active provider, Cloud endpoint `https://ollama.com/v1` | OpenAI-compatible | bearer in Cloud mode | native Ollama `/api/tags` |

Core providers cannot be removed or renamed into another identity. Custom providers can be added, configured, probed, queried, selected, and removed. Select another provider before removing an active custom provider.

Use these commands to inspect canonical state:

```bash
cm llm status
cm llm provider list
cm llm provider show ollama
```

Provider outages, authentication failures, rate limits, and malformed remote responses do not automatically select another provider or rewrite persisted provider configuration.

## Active-provider settings

The generic configuration surface exposes virtual aliases for the currently active provider:

| Setting | Meaning |
| --- | --- |
| `llm.provider` | active provider ID |
| `llm.base_url` | active provider base URL |
| `llm.model` | active provider model |
| `llm.api_key` | managed credential for the active provider |
| `llm.api_key_configured` | derived credential-presence state |

Provider-specific settings use `llm.providers[<id>].*`, including `name`, `protocol`, `base_url`, `model`, `api_key`, `api_key_configured`, `auth_mode`, `discovery`, and the read-only `core` identity flag.

For example:

```bash
cm config get llm.provider
cm config set llm.provider ollama
cm config set llm.model qwen3:8b
cm config get 'llm.providers[ollama].base_url'
```

When one atomic setting transaction changes `llm.provider` and another active-provider alias, selection is staged first, so the alias targets the newly selected provider. `llm.provider` itself cannot be unset.

## Ollama

A fresh catalog selects Ollama in Cloud mode. Model selection remains explicit, so configure a model before inference or features such as Approval Explain can become ready. Cloud mode requires an Ollama API key; local mode does not.

Configure the credential without putting it on the command line:

```bash
cm llm ollama key set
cm llm ollama status
cm llm ollama probe
cm llm ollama models --limit 20
```

Select another explicit model when needed:

```bash
cm llm ollama models --search qwen --sort modified:desc --limit 20
cm llm ollama model <model-id>
cm llm ollama use
```

Ollama model discovery uses native `/api/tags`. Query dimensions are capability-checked and unsupported filters or ranking/recommendation requests fail explicitly instead of being guessed.

## Ollama Cloud and local mode

A fresh Ollama core provider starts in Cloud mode:

- base URL: `https://ollama.com/v1`;
- bearer authentication;
- native Ollama `/api/tags` model discovery.

Configure Cloud usage with protected credential input:

```bash
cm llm ollama mode cloud
cm llm ollama key set
cm llm ollama models --refresh
cm llm ollama model <model-id>
cm llm ollama probe
cm llm ollama use
```

For a local Ollama daemon:

```bash
cm llm ollama mode local
cm llm ollama models --refresh
cm llm ollama model qwen3:8b
cm llm ollama probe
cm llm ollama use
```

Local mode uses `http://localhost:11434/v1` and no credential. Inference uses the OpenAI-compatible `/v1` endpoint while model discovery remains the native Ollama tags API. Ollama model IDs retain their native names/tags, such as `qwen3:8b`.

Fresh defaults do not overwrite persisted state on restart or reconciliation. A previously selected local, Cloud, or custom Ollama endpoint/model remains persisted. Running `cm llm ollama mode local` or `cloud` is an explicit request to restore that mode's endpoint/auth defaults; it preserves the configured model and does not change which provider is active.

## Custom OpenAI- and Anthropic-compatible providers

Add an OpenAI-compatible provider:

```bash
cm llm provider add acme-openai \
  --name "Acme OpenAI" \
  --protocol openai \
  --base-url https://llm.example.invalid/v1 \
  --model acme/model-1 \
  --auth bearer \
  --discovery openai-models

cm llm provider key set acme-openai
cm llm probe acme-openai
cm llm use acme-openai
```

Add an Anthropic-compatible provider:

```bash
cm llm provider add acme-anthropic \
  --name "Acme Anthropic" \
  --protocol anthropic \
  --base-url https://anthropic.example.invalid \
  --model acme-model-1 \
  --auth x-api-key \
  --discovery none

cm llm provider key set acme-anthropic
cm llm probe acme-anthropic
```

The OpenAI-compatible adapter supports `none` or `bearer` authentication. The Anthropic-compatible adapter supports `none` or `x-api-key`. Use `discovery none` when a custom provider does not expose a supported model-list endpoint; exact model IDs can still be configured manually.

Custom provider configuration is independent of active selection:

```bash
cm llm provider configure acme-openai --model acme/model-2
cm llm probe acme-openai
cm llm use ollama
cm llm provider remove acme-openai
```

Configure, model-query, probe, credential, and remove operations do not implicitly select a provider. Selection changes only through an explicit `cm llm use <provider_id>` or equivalent interface action.

## Protect API keys

LLM API keys are managed secrets. Raw values are stored by CodeMCP's canonical secret store and are not persisted in the ordinary provider JSON or emitted by normal status/config output. Interfaces show configured state and, where useful, a masked preview.

For interactive CLI use, omit the secret value:

```bash
cm llm ollama key set
cm llm provider key set acme-openai
cm config set llm.api_key
```

The terminal uses protected input and masks typed characters. For automation, prefer an environment variable or standard input:

```bash
cm llm ollama key set --from-env OLLAMA_API_KEY
printf '%s\n' "$ACME_LLM_API_KEY" | cm llm provider key set acme-openai
```

Passing a secret as a positional command argument remains supported for compatibility, but it is discouraged because shells, histories, and process listings can retain command-line arguments.

Clear a credential explicitly:

```bash
cm llm ollama key clear
cm llm provider key clear acme-openai
cm config unset llm.api_key
```

Portable config export/import excludes managed secret-store values. Removing a custom provider also removes its owned credential. Uninitialization/purge uses the same canonical secret inventory.

## Model catalog queries

`cm llm models [provider_id]` queries a provider explicitly; when the ID is omitted it uses the active provider. The core-provider shortcut `cm llm ollama models` uses the same query engine.

Common query controls include:

| Purpose | CLI controls |
| --- | --- |
| Search/exact identity | `--search`, repeatable `--id`, repeatable `--author` |
| Price/context | `--free`, `--paid`, `--min-context`, `--max-context`, `--min-prompt-price`, `--max-prompt-price`, `--min-completion-price`, `--max-completion-price` |
| Capabilities | repeatable `--capability`, `--parameter`, `--input`, `--output` |
| Time | RFC3339 `--created-after`, `--created-before`, `--modified-after`, `--modified-before` |
| Ordering | repeatable `--sort field[:asc|desc]` |
| Result window | `--offset`, `--limit`, 1-based inclusive `--range start:end`, `--all`, `--count` |
| Refresh | `--refresh` |
| Optional provider enrichment | `--rank`, `--window`, `--recommend-for` when the provider exposes it |
| Ollama metadata | repeatable `--family`, `--format`, `--quantization`, plus `--min-parameters`, `--max-parameters`, `--min-size`, `--max-size` |

Examples:

```bash
cm llm models --search qwen --sort context:desc --limit 20
cm llm ollama models --family qwen --sort parameter-size:desc --range 1:10
cm llm models --count
```

Without explicit ranking/recommendation/sort, results have a stable ID ordering. Provider-specific dimensions are capability-checked: requesting an unsupported filter, sort, rank, or recommendation returns an explicit unsupported-query error rather than silently ignoring the request.

Ranking/recommendation is not synthesized for Ollama. If a custom backend does not provide explicit enrichment metadata, rank/recommendation queries return an unsupported-query error.

## Approval Explain

Approval Explain is an informational LLM consumer attached to human approval review. Its mode is `off`, `manual`, or `auto`:

```bash
cm request explain mode off
cm request explain mode manual
cm request explain mode auto
cm request explain status
```

Enabling `manual` or `auto` requires the active LLM provider to be configured and to pass an explicit readiness probe before the mode is persisted.

In `manual` mode, a reviewer requests an explanation explicitly. In `auto` mode, generation starts only after the approval request already exists and is pending. A failed explanation stays failed until an explicit retry:

```bash
cm request explain <request-id>
cm request explain retry <request-id>
```

The explanation is generated from the canonical exact command/action after secret redaction. The agent-authored request title/summary remains separate provenance and is not the source of truth for the generated explanation.

Generated text is fallible and non-authoritative. Explain cannot approve or deny a request, change deterministic or semantic risk classification, alter retry binding, create a runtime grant, or extend approval lifetime. Approve/Deny remains usable when the LLM is unavailable or the explanation fails. The requesting MCP agent does not receive an Explain capability that could shape the human-facing result.

## Security and semantic boundaries

Provider URLs may not embed credentials. API keys remain in the managed secret store. LLM trace/activity metadata does not use prompt or response bodies as ordinary observability fields.

Sending an LLM request necessarily sends the feature's prepared input to the configured remote provider, so choose provider endpoints according to your data-handling requirements. CodeMCP does not silently fail over to another provider when the configured provider fails.

General LLM inference is not a security decision mechanism. Any future semantic command classifier must integrate through CodeMCP's existing semantic/risk-classification contract, where deterministic deny/approval rules retain their precedence; it must not treat an arbitrary LLM response as approval authority.

See [Security](security.md) for the broader trust model and [Configuration](configuration.md) for config-root, secret-store, export/import, and purge behavior.
