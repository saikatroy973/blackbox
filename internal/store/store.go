// Package store persists spans and serves trace queries.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

// ErrNotFound is returned when a trace does not exist.
var ErrNotFound = errors.New("not found")

// TraceSummary is a per-trace rollup for trace listings.
type TraceSummary struct {
	TraceID    string    `json:"trace_id"`
	RootName   string    `json:"root_name"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	SpanCount  int       `json:"span_count"`
	ErrorCount int       `json:"error_count"`
}

// TraceFilter narrows ListTraces results.
type TraceFilter struct {
	Limit  int
	Offset int
	// Status filters by outcome: "" (all), "ok", or "error".
	Status string
}

// Store persists spans.
type Store interface {
	// InsertSpans writes a batch of spans atomically.
	InsertSpans(ctx context.Context, spans []span.Span) error
	// GetTrace returns all spans of a trace ordered by start time.
	// It returns ErrNotFound if the trace does not exist.
	GetTrace(ctx context.Context, traceID string) ([]span.Span, error)
	// ListTraces returns trace summaries, most recent first.
	ListTraces(ctx context.Context, f TraceFilter) ([]TraceSummary, error)
	// Close releases the underlying resources.
	Close() error
}
