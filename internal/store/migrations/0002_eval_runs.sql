CREATE TABLE IF NOT EXISTS eval_runs (
	id             TEXT PRIMARY KEY,
	suite          TEXT NOT NULL,
	started_at     INTEGER NOT NULL,
	duration_ms    INTEGER NOT NULL,
	total          INTEGER NOT NULL,
	passed         INTEGER NOT NULL,
	failed         INTEGER NOT NULL,
	total_cost_usd REAL NOT NULL DEFAULT 0,
	results        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_eval_runs_suite ON eval_runs (suite, started_at DESC);
