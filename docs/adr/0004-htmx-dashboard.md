# 0004 — Server-rendered HTMX dashboard with SSE, not a SPA

Status: accepted

## Context

The dashboard is read-mostly observability UI: lists, waterfall views,
payload inspectors, eval runs, trace diffs. A JS framework would add a
build toolchain, a second language, and duplicate types across the wire.

## Decision

Render HTML on the server with `html/template` from embedded files
(`web/`). Interactivity uses HTMX (vendored, no build step): fragment
refreshes, click-to-inspect span panels. Live updates use the browser's
native `EventSource` against `GET /v1/events` (server-sent events), with a
30-second HTMX poll as fallback. Fragments are served from the same route
as the page, keyed on the `HX-Request` header.

## Consequences

- One language, one artifact; templates are testable with `httptest`.
- No client state to synchronize; the URL is the state.
- Rich client-side interactions (drag/zoom waterfalls) would need custom
  JS if ever required.
