CREATE TABLE IF NOT EXISTS spans (
	trace_id       TEXT NOT NULL,
	span_id        TEXT NOT NULL,
	parent_span_id TEXT NOT NULL DEFAULT '',
	name           TEXT NOT NULL,
	kind           TEXT NOT NULL,
	start_time     BIGINT NOT NULL,
	end_time       BIGINT NOT NULL,
	status         TEXT NOT NULL DEFAULT 'ok',
	error          TEXT NOT NULL DEFAULT '',
	attributes     TEXT NOT NULL DEFAULT '{}',
	input          TEXT NOT NULL DEFAULT '',
	output         TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans (trace_id, start_time);
CREATE INDEX IF NOT EXISTS idx_spans_start ON spans (start_time DESC);
