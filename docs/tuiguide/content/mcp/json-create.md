# Upstream JSON Creation

When adding an Upstream, the create page has **Form** and **JSON** modes. JSON mode uses a multiline editor and the same canonical parser/domain validation as non-TUI Upstream configuration.

## Accepted shape

The canonical schema supports Upstream definitions, including the common MCP-client `{ "mcpServers": { ... } }` shape as well as the parser-supported raw/array forms. Transport can be inferred from `command` (stdio) or `url` (HTTP) when unambiguous. Conflicting command+URL input without a clear transport is rejected.

## Multiple Upstreams

JSON mode may create multiple Upstreams in one submission. Creation uses create-only atomic batch semantics: the complete batch is validated first, duplicate IDs and already-existing IDs are rejected, and persistence failure rolls back the whole batch rather than leaving partial Upstreams.

## Form/JSON draft synchronization

Form and JSON have separate drafts and baselines. Switching modes synchronizes only when the source changed and the destination has not independently changed since the last synchronization. If both drafts diverged, the destination is preserved instead of being overwritten silently.

A single-Upstream JSON draft can round-trip back to Form. A multi-Upstream JSON draft cannot be silently collapsed into a single Form Upstream.

## Errors and secrets

Parse/domain errors keep the exact JSON draft open. Error formatting is designed not to echo secret values. Enter inserts newlines and Backspace belongs to the JSON textarea rather than global Back navigation.
