# 0005 — Evals as YAML suites with file-based baselines

Status: accepted

## Context

Agent changes (prompts, models, tools) need regression gates in CI. Hosted
eval products require accounts and API keys; ad-hoc scripts rot. The test
runner already exists: the demo agent behind a mock LLM is deterministic,
and every case records a trace.

## Decision

Eval suites are plain YAML files (`evals/*.yaml`): cases with a question
and declarative expectations (output contains/not-contains, tools called,
max steps, max cost, max latency). `blackbox eval run` executes them,
records one trace per case (tagged `blackbox.eval_suite` /
`blackbox.eval_case`), persists the suite run, and can save/compare a JSON
baseline (`-save`/`-baseline`). Exit code is non-zero when a case fails or
a previously passing case regresses.

## Consequences

- CI gating is a single command; no service dependency.
- Assertions stay declarative and reviewable in PRs.
- LLM-as-judge and statistical scoring are future extensions, not v1.
