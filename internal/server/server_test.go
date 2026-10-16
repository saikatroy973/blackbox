package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/server"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h, err := server.New(server.Config{Version: "test", Store: st})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return h
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestDashboardAndStaticRoutes(t *testing.T) {
	h := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ui status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("static status = %d, want 200", rec.Code)
	}
}

func TestRootReportsVersion(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["version"] != "test" {
		t.Fatalf("version = %q, want test", body["version"])
	}
}

func TestIngestToStoreRoundtrip(t *testing.T) {
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h, err := server.New(server.Config{Version: "test", Store: st})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	now := time.Now()
	s := span.Span{
		TraceID:   "trace-e2e",
		SpanID:    "span-1",
		Name:      "agent",
		Kind:      span.KindAgentRun,
		StartTime: now,
		EndTime:   now.Add(time.Second),
		Status:    span.StatusOK,
	}
	body, err := json.Marshal(map[string]any{"spans": []span.Span{s}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/spans", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202 (body: %s)", rec.Code, rec.Body)
	}

	got, err := st.GetTrace(context.Background(), "trace-e2e")
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(got) != 1 || got[0].Name != "agent" {
		t.Fatalf("stored spans = %+v", got)
	}
}
