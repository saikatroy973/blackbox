# Architecture decision records

Short records of the load-bearing decisions in Blackbox, in the style of
Michael Nygard's ADRs. Format: context, decision, consequences.

| # | Title | Status |
|---|-------|--------|
| [0001](0001-native-span-format.md) | Native span wire format instead of OTLP | accepted |
| [0002](0002-sqlite-and-postgres.md) | One Store interface, SQLite and Postgres implementations | accepted |
| [0003](0003-cassette-replay.md) | Replay from cassettes extracted after the fact | accepted |
| [0004](0004-htmx-dashboard.md) | Server-rendered HTMX dashboard with SSE, not a SPA | accepted |
| [0005](0005-yaml-evals.md) | Evals as YAML suites with file-based baselines | accepted |
