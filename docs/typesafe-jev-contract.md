# TypeSafe / Jev wire contract

Verified against the live TypeSafe documentation on **2026-09-27**. This document
freezes only the provider contract CodeMCP may rely on. It does not imply that the
TypeSafe integration is enabled or configured.

## Endpoints and authentication

- API base URL: `https://api.typesafe.ai`
- Evaluation: `POST /v1/systemone`
- Model discovery/probe: `GET /v1/models`
- Authentication: `Authorization: Bearer <API_KEY>`
- JSON requests use `Content-Type: application/json`.
- The provider SDK documents `TYPESAFE_API_KEY`, `TYPESAFE_BASE_URL`, and
  `TYPESAFE_DEFAULT_MODEL`; CodeMCP does not implicitly adopt those environment
  variables as configuration authority.

## Models

- `jev-latest` is the stable alias and currently resolves to `jev-1.13.0`.
- `jev-preview` currently resolves to the same version.
- Aliases are movable. Any threshold calibrated against a specific model version
  must record the version returned in the response and may pin a versioned ID.
- `GET /v1/models` is the capability probe for account-visible model aliases.

## Request and response shape

`POST /v1/systemone` accepts:

- `state`: string, JSON object, or JSON array containing text-oriented state;
- `model`: required model name;
- `questions`: a map of caller-defined IDs to typed questions.

Supported primitives are exactly:

- `noul`: yes/no probability in `noul`; there is no separate confidence field;
- `choice`: selected `choice`, complete `probabilities`, and `confidence`;
- `score`: probability-weighted `score`, numeric-keyed `legend`,
  `probabilities`, and `confidence`.

The response also includes the actual model ID and token usage
(`input_tokens`, `output_tokens`). Multiple questions share the same state and are
evaluated independently.

Current documented provider limits:

- Choice: at most 255 options;
- Score: 2 to 10 levels;
- Jev 1.13 context: 64k tokens for the request and 32k tokens for state plus the
  longest individual question;
- Jev 1.13 input is text-only; non-text media must be transformed before use;
- published service limits at verification time: 250,000 tokens/second and
  1,200 requests/minute. TypeSafe explicitly states these limits may change.

CodeMCP must therefore keep stricter local byte/question/concurrency limits and
must not treat the published rate limits as a durable allowance.

## Error, timeout, and retry contract

Documented HTTP errors include:

- `401`: missing/invalid API key;
- `422`: invalid request;
- `429`: rate limit exceeded;
- `529`: provider overloaded;
- other `5xx`, transport failures, and timeouts remain provider/transport errors.

TypeSafe documents exponential backoff for `429` and `529`. The SDK retry policy
can also be configured for selected HTTP statuses, connection errors, and timeout
errors, and can honor `Retry-After` / `retry-after-ms`. The Python SDK default
HTTP-operation timeout is 10 seconds.

CodeMCP must apply a smaller bounded retry policy inside its own consumer deadline.
Cancellation must stop retrying. Provider error bodies, request content, API keys,
and raw responses must not be copied into normal logs or diagnostics.

## Privacy boundary

Remote evaluation necessarily sends the selected `state`, `instructions`, and
`criteria` to TypeSafe. Before a request leaves CodeMCP:

- authorization and workspace scope filtering happen locally;
- consumers minimize state to the evidence required for the judgment;
- secrets and credentials are removed locally;
- binary/image/audio/video data is never sent directly;
- raw remote request/response bodies are not persisted as CodeMCP history,
  telemetry, or diagnostics.

TypeSafe states that Jev is not trained on customer requests or responses. Its
legal documentation describes normal data retention and offers zero-data-retention
for enterprise customers. CodeMCP must not assume ZDR unless the deployment has
explicitly established it.

## Risk classification capability

The verified API exposes general `Noul`, `Choice`, and `Score` judgments. TypeSafe's
guardrails cookbook demonstrates that applications can compose hazard questions and
severity scores, then make their own policy decisions.

There is **no documented dedicated command/tool risk-classification primitive or
endpoint** that returns CodeMCP's required risk class, confidence, bounded evidence,
and stable contract. Therefore the TypeSafe `RiskClassifier` adapter remains
unavailable. CodeMCP must not infer that capability merely by prompting generic
Choice/Score/Noul primitives.

If TypeSafe later publishes an explicit suitable contract, it must be separately
verified and fixture-tested before enabling that adapter.

## Sources

- https://docs.typesafe.ai/llms.txt
- https://docs.typesafe.ai/api
- https://docs.typesafe.ai/models
- https://docs.typesafe.ai/primitives
- https://docs.typesafe.ai/primitives/choice
- https://docs.typesafe.ai/primitives/score
- https://docs.typesafe.ai/primitives/noul
- https://docs.typesafe.ai/sdk/python/api/constants
- https://docs.typesafe.ai/sdk/python/api/retries
- https://docs.typesafe.ai/sdk/python/api/exceptions
- https://docs.typesafe.ai/cookbooks/llm_guardrails
- https://docs.typesafe.ai/legal
