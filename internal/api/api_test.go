package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/api"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func seed(t *testing.T) (store.Store, http.Handler) {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	spans := []span.Span{
		{
			TraceID: "trace-1", SpanID: "root", Name: "agent", Kind: span.KindAgentRun,
			StartTime: now, EndTime: now.Add(time.Second), Status: span.StatusOK,
		},
		{
			TraceID: "trace-1", SpanID: "child", ParentSpanID: "root", Name: "plan", Kind: span.KindLLMCall,
			StartTime: now.Add(100 * time.Millisecond), EndTime: now.Add(500 * time.Millisecond), Status: span.StatusError,
		},
		{
			TraceID: "trace-2", SpanID: "root", Name: "other", Kind: span.KindAgentRun,
			StartTime: now.Add(time.Hour), EndTime: now.Add(time.Hour + time.Second), Status: span.StatusOK,
		},
	}
	if err := st.InsertSpans(context.Background(), spans); err != nil {
		t.Fatalf("insert: %v", err)
	}

	mux := http.NewServeMux()
	api.New(st).Routes(mux)
	return st, mux
}

func TestListTracesJSON(t *testing.T) {
	_, h := seed(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Traces []store.TraceSummary `json:"traces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Traces) != 2 {
		t.Fatalf("traces = %d, want 2", len(body.Traces))
	}
	if body.Traces[0].TraceID != "trace-2" {
		t.Fatalf("first trace = %q, want most recent", body.Traces[0].TraceID)
	}
}

func TestListTracesFilters(t *testing.T) {
	_, h := seed(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces?status=error", nil))
	var body struct {
		Traces []store.TraceSummary `json:"traces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Traces) != 1 || body.Traces[0].TraceID != "trace-1" {
		t.Fatalf("error filter = %+v", body.Traces)
	}
}

func TestListTracesBadParams(t *testing.T) {
	_, h := seed(t)

	for _, query := range []string{"?status=bogus", "?limit=0", "?limit=abc", "?offset=-1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces"+query, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q: status = %d, want 400", query, rec.Code)
		}
	}
}

func TestGetTraceJSON(t *testing.T) {
	_, h := seed(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Spans []span.Span `json:"spans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Spans) != 2 || body.Spans[0].SpanID != "root" || body.Spans[1].SpanID != "child" {
		t.Fatalf("spans = %+v", body.Spans)
	}
}

func TestGetTraceNotFound(t *testing.T) {
	_, h := seed(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
