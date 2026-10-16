package store

import (
	"context"
	"encoding/json"
	"time"
)

// EvalRun is a persisted eval suite execution.
type EvalRun struct {
	ID           string          `json:"id"`
	Suite        string          `json:"suite"`
	StartedAt    time.Time       `json:"started_at"`
	DurationMS   int64           `json:"duration_ms"`
	Total        int             `json:"total"`
	Passed       int             `json:"passed"`
	Failed       int             `json:"failed"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	Results      json.RawMessage `json:"results,omitempty"`
}

// EvalRunFilter narrows ListEvalRuns results.
type EvalRunFilter struct {
	// Suite limits runs to one suite when non-empty.
	Suite string
	Limit int
}

const insertEvalRunSQL = `
INSERT OR REPLACE INTO eval_runs (
	id, suite, started_at, duration_ms, total, passed, failed, total_cost_usd, results
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

// InsertEvalRun persists an eval run.
func (s *SQLite) InsertEvalRun(ctx context.Context, run EvalRun) error {
	results := run.Results
	if len(results) == 0 {
		results = json.RawMessage("[]")
	}
	_, err := s.db.ExecContext(ctx, insertEvalRunSQL,
		run.ID, run.Suite, run.StartedAt.UnixNano(), run.DurationMS,
		run.Total, run.Passed, run.Failed, run.TotalCostUSD, string(results),
	)
	return err
}

const listEvalRunsSQL = `
SELECT id, suite, started_at, duration_ms, total, passed, failed, total_cost_usd, results
FROM eval_runs
WHERE (?1 = '' OR suite = ?1)
ORDER BY started_at DESC
LIMIT ?2`

// ListEvalRuns returns eval runs, most recent first.
func (s *SQLite) ListEvalRuns(ctx context.Context, f EvalRunFilter) ([]EvalRun, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, listEvalRunsSQL, f.Suite, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EvalRun
	for rows.Next() {
		var (
			run       EvalRun
			startedNS int64
			results   string
		)
		if err := rows.Scan(
			&run.ID, &run.Suite, &startedNS, &run.DurationMS,
			&run.Total, &run.Passed, &run.Failed, &run.TotalCostUSD, &results,
		); err != nil {
			return nil, err
		}
		run.StartedAt = time.Unix(0, startedNS).UTC()
		run.Results = json.RawMessage(results)
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []EvalRun{}
	}
	return out, nil
}
