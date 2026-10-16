package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/saikat/blackbox/pkg/span"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLite is a Store backed by a SQLite database.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the SQLite database at dsn and
// applies pending migrations. Use ":memory:" for an ephemeral database.
func OpenSQLite(ctx context.Context, dsn string) (*SQLite, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite serializes writers; a single connection avoids SQLITE_BUSY
	// between concurrent requests.
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &SQLite{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for i, name := range names {
		next := i + 1
		if next <= version {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
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
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", next)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: set user_version: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

const insertSpanSQL = `
INSERT OR REPLACE INTO spans (
	trace_id, span_id, parent_span_id, name, kind,
	start_time, end_time, status, error, attributes, input, output
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// InsertSpans writes a batch of spans in a single transaction.
func (s *SQLite) InsertSpans(ctx context.Context, spans []span.Span) error {
	if len(spans) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSpanSQL)
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
			string(attrsJSON), []byte(sp.Input), []byte(sp.Output),
		); err != nil {
			return fmt.Errorf("insert span %s: %w", sp.SpanID, err)
		}
	}
	return tx.Commit()
}

const traceSpansSQL = `
SELECT trace_id, span_id, parent_span_id, name, kind,
       start_time, end_time, status, error, attributes, input, output
FROM spans
WHERE trace_id = ?
ORDER BY start_time, span_id`

// GetTrace returns all spans of a trace ordered by start time.
func (s *SQLite) GetTrace(ctx context.Context, traceID string) ([]span.Span, error) {
	rows, err := s.db.QueryContext(ctx, traceSpansSQL, traceID)
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
			input      []byte
			output     []byte
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
		sp.Input = input
		sp.Output = output
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

const listTracesSQL = `
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
	?1 = ''
	OR (?1 = 'error' AND SUM(CASE WHEN s.status = 'error' THEN 1 ELSE 0 END) > 0)
	OR (?1 = 'ok' AND SUM(CASE WHEN s.status = 'error' THEN 1 ELSE 0 END) = 0)
)
ORDER BY start_time DESC
LIMIT ?2 OFFSET ?3`

// ListTraces returns trace summaries, most recent first.
func (s *SQLite) ListTraces(ctx context.Context, f TraceFilter) ([]TraceSummary, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	status := f.Status
	if status != "ok" && status != "error" {
		status = ""
	}
	rows, err := s.db.QueryContext(ctx, listTracesSQL, status, limit, f.Offset)
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
