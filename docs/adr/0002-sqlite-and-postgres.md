# 0002 — One Store interface, SQLite and Postgres implementations

Status: accepted

## Context

The project needs a zero-setup local experience (single binary, no
services) and a credible production story. Running two divergent storage
paths invites behavioral drift.

## Decision

Define a single `store.Store` interface. Ship two implementations:
SQLite (`modernc.org/sqlite`, pure Go, WAL, single writer connection) for
development and single-node use, and Postgres (`pgx`) for production. The
CLI picks the implementation from the `-db` flag: a path selects SQLite,
a `postgres://` DSN selects Postgres. Both run the same embedded migration
files (tracked per dialect) and are exercised by a shared conformance test
suite; the Postgres suite is skipped locally unless `TEST_POSTGRES_DSN` is
set and always runs in CI.

## Consequences

- Identical behavior is enforced by tests, not by discipline.
- Some SQL is duplicated per dialect (placeholder syntax, upsert clauses).
- Analytics-heavy queries (dashboard rollups) stay simple enough for both.
