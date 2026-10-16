# 0001 — Native span wire format instead of OTLP

Status: accepted

## Context

Blackbox needs traces rich enough to replay agent runs: exact LLM messages,
tool arguments, and their outputs, plus economics (tokens, cost). The
OpenTelemetry GenAI conventions are still evolving and OTLP payloads don't
preserve everything we need for deterministic replay (e.g. full prompt
payloads are often dropped for cost reasons).

## Decision

Record spans in a Blackbox-native JSON wire format (`pkg/span`) sent to
`POST /v1/spans`. Attribute keys follow OpenTelemetry GenAI semantic
conventions where they exist (`gen_ai.request.model`, `gen_ai.usage.*`),
with `blackbox.*` extensions (`blackbox.cost_usd`, `blackbox.replay_of`,
`blackbox.truncated`). Payloads are stored verbatim, capped at 256 KiB per
payload with a truncation flag.

## Consequences

- Replay and evals can rely on exact payloads being present.
- Ingest stays a single dependency-free HTTP endpoint; SDKs are thin.
- Interop with pure-OTLP pipelines requires a future translation layer
  (possible, not built yet).
