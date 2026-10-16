package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/saikat/blackbox/pkg/span"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/postgres/*.sql
var postgresMigrationsFS embed.FS

// Postgres is a Store backed by a PostgreSQL database.
type Postgres struct {
	db *sql.DB
}

// OpenPostgres opens the Postgres database at dsn and applies pending
// migrations.
func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Postgres{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Postgres) Close() error {
	return s.db.Close()
}

func (s *Postgres) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(postgresMigrationsFS, "migrations/postgres")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for i, name := range names {
		version := i + 1
		var one int
		err := s.db.QueryRowContext(ctx,
			"SELECT 1 FROM schema_migrations WHERE version = $1", version).Scan(&one)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := fs.ReadFile(postgresMigrationsFS, "migrations/postgres/"+name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", version, name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: record: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

const insertSpanPostgresSQL = `
INSERT INTO spans (
	trace_id, span_id, parent_span_id, name, kind,
	start_time, end_time, status, error, attributes, input, output
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (trace_id, span_id) DO UPDATE SET
	parent_span_id = EXCLUDED.parent_span_id,
	name           = EXCLUDED.name,
	kind           = EXCLUDED.kind,
	start_time     = EXCLUDED.start_time,
	end_time       = EXCLUDED.end_time,
	status         = EXCLUDED.status,
	error          = EXCLUDED.error,
	attributes     = EXCLUDED.attributes,
	input          = EXCLUDED.input,
	output         = EXCLUDED.output`

// InsertSpans writes a batch of spans in a single transaction.
func (s *Postgres) InsertSpans(ctx context.Context, spans []span.Span) error {
	if len(spans) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSpanPostgresSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sp := range spans {
		attrs := sp.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrsJSON, err := json.Marshal(attrs)
		if err != nil {
			return fmt.Errorf("marshal attributes of span %s: %w", sp.SpanID, err)
		}
		status := sp.Status
		if status == "" {
			status = span.StatusOK
		}
		if _, err := stmt.ExecContext(ctx,
			sp.TraceID, sp.SpanID, sp.ParentSpanID, sp.Name, string(sp.Kind),
			sp.StartTime.UnixNano(), sp.EndTime.UnixNano(), string(status), sp.Error,
			string(attrsJSON), string(sp.Input), string(sp.Output),
		); err != nil {
			return fmt.Errorf("insert span %s: %w", sp.SpanID, err)
		}
	}
	return tx.Commit()
}

const traceSpansPostgresSQL = `
SELECT trace_id, span_id, parent_span_id, name, kind,
       start_time, end_time, status, error, attributes, input, output
FROM spans
WHERE trace_id = $1
ORDER BY start_time, span_id`

// GetTrace returns all spans of a trace ordered by start time.
func (s *Postgres) GetTrace(ctx context.Context, traceID string) ([]span.Span, error) {
	rows, err := s.db.QueryContext(ctx, traceSpansPostgresSQL, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []span.Span
	for rows.Next() {
		var (
			sp         span.Span
			kind       string
			status     string
			attributes string
			input      string
			output     string
			startNS    int64
			endNS      int64
		)
		if err := rows.Scan(
			&sp.TraceID, &sp.SpanID, &sp.ParentSpanID, &sp.Name, &kind,
			&startNS, &endNS, &status, &sp.Error, &attributes, &input, &output,
		); err != nil {
			return nil, err
		}
		sp.Kind = span.Kind(kind)
		sp.Status = span.Status(status)
		sp.StartTime = time.Unix(0, startNS).UTC()
		sp.EndTime = time.Unix(0, endNS).UTC()
		if attributes != "" {
			if err := json.Unmarshal([]byte(attributes), &sp.Attributes); err != nil {
				return nil, fmt.Errorf("unmarshal attributes of span %s: %w", sp.SpanID, err)
			}
		}
		sp.Input = []byte(input)
		sp.Output = []byte(output)
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

const listTracesPostgresSQL = `
SELECT
	s.trace_id,
	COALESCE((
		SELECT r.name FROM spans r
		WHERE r.trace_id = s.trace_id AND r.parent_span_id = ''
		ORDER BY r.start_time LIMIT 1
	), '') AS root_name,
	MIN(s.start_time) AS start_time,
	MAX(s.end_time) AS end_time,
	COUNT(*) AS span_count,
	SUM(CASE WHEN s.status = 'error' THEN 1 ELSE 0 END) AS error_count
FROM spans s
GROUP BY s.trace_id
HAVING (
	$1::text = ''
	OR ($1::text = 'error' AND SUM(CASE WHEN s.status = 'error' THEN 1 ELSE 0 END) > 0)
	OR ($1::text = 'ok' AND SUM(CASE WHEN s.status = 'error' THEN 1 ELSE 0 END) = 0)
)
ORDER BY start_time DESC
LIMIT $2::int OFFSET $3::int`

// ListTraces returns trace summaries, most recent first.
func (s *Postgres) ListTraces(ctx context.Context, f TraceFilter) ([]TraceSummary, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	status := f.Status
	if status != "ok" && status != "error" {
		status = ""
	}
	rows, err := s.db.QueryContext(ctx, listTracesPostgresSQL, status, limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TraceSummary
	for rows.Next() {
		var (
			ts      TraceSummary
			startNS int64
			endNS   int64
		)
		if err := rows.Scan(&ts.TraceID, &ts.RootName, &startNS, &endNS, &ts.SpanCount, &ts.ErrorCount); err != nil {
			return nil, err
		}
		ts.StartTime = time.Unix(0, startNS).UTC()
		ts.EndTime = time.Unix(0, endNS).UTC()
		out = append(out, ts)
	}
	return out, rows.Err()
}

const insertEvalRunPostgresSQL = `
INSERT INTO eval_runs (
	id, suite, started_at, duration_ms, total, passed, failed, total_cost_usd, results
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
	suite          = EXCLUDED.suite,
	started_at     = EXCLUDED.started_at,
	duration_ms    = EXCLUDED.duration_ms,
	total          = EXCLUDED.total,
	passed         = EXCLUDED.passed,
	failed         = EXCLUDED.failed,
	total_cost_usd = EXCLUDED.total_cost_usd,
	results        = EXCLUDED.results`

// InsertEvalRun persists an eval run.
func (s *Postgres) InsertEvalRun(ctx context.Context, run EvalRun) error {
	results := run.Results
	if len(results) == 0 {
		results = json.RawMessage("[]")
	}
	_, err := s.db.ExecContext(ctx, insertEvalRunPostgresSQL,
		run.ID, run.Suite, run.StartedAt.UnixNano(), run.DurationMS,
		run.Total, run.Passed, run.Failed, run.TotalCostUSD, string(results),
	)
	return err
}

const listEvalRunsPostgresSQL = `
SELECT id, suite, started_at, duration_ms, total, passed, failed, total_cost_usd, results
FROM eval_runs
WHERE ($1::text = '' OR suite = $1::text)
ORDER BY started_at DESC
LIMIT $2::int`

// ListEvalRuns returns eval runs, most recent first.
func (s *Postgres) ListEvalRuns(ctx context.Context, f EvalRunFilter) ([]EvalRun, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, listEvalRunsPostgresSQL, f.Suite, limit)
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

const getEvalRunPostgresSQL = `
SELECT id, suite, started_at, duration_ms, total, passed, failed, total_cost_usd, results
FROM eval_runs
WHERE id = $1`

// GetEvalRun returns a single eval run by ID.
func (s *Postgres) GetEvalRun(ctx context.Context, id string) (EvalRun, error) {
	var (
		run       EvalRun
		startedNS int64
		results   string
	)
	err := s.db.QueryRowContext(ctx, getEvalRunPostgresSQL, id).Scan(
		&run.ID, &run.Suite, &startedNS, &run.DurationMS,
		&run.Total, &run.Passed, &run.Failed, &run.TotalCostUSD, &results,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return EvalRun{}, ErrNotFound
	}
	if err != nil {
		return EvalRun{}, err
	}
	run.StartedAt = time.Unix(0, startedNS).UTC()
	run.Results = json.RawMessage(results)
	return run, nil
}
