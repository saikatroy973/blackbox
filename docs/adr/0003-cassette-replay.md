# 0003 — Replay from cassettes extracted after the fact

Status: accepted

## Context

Deterministic replay ("time travel debugging") is usually implemented by
writing cassettes as the agent runs: the SDK tees every LLM and tool
interaction to disk. That adds a mode to agent code and a second storage
path to keep consistent.

## Decision

Extract cassettes from already-recorded traces. `internal/replay` rebuilds
a keyed cassette set from the stored `llm.call` / `tool.call` spans:
sha256 of model + canonicalized messages, and tool name + canonicalized
args. Replay drives the same agent code with a provider and tool registry
that serve the cassette entries. A cassette miss is a divergence: the
replay aborts and reports the exact step, and the replay itself is
recorded as a new trace tagged `blackbox.replay_of`.

## Consequences

- The tracer SDK stays write-only; no recording mode, no extra state.
- Replay is only as complete as what was traced — truncation at 256 KiB
  or dropped payloads show up as divergences, not silent skips.
- Canonicalization is decode-then-remark (key order and whitespace
  insensitive); genuinely different content must diverge.
