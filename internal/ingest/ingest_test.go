package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

type fakeStore struct {
	spans []span.Span
	err   error
}

func (f *fakeStore) InsertSpans(_ context.Context, spans []span.Span) error {
	if f.err != nil {
		return f.err
	}
	f.spans = append(f.spans, spans...)
	return nil
}

func (f *fakeStore) GetTrace(context.Context, string) ([]span.Span, error) {
	return nil, store.ErrNotFound
}

func (f *fakeStore) ListTraces(context.Context, store.TraceFilter) ([]store.TraceSummary, error) {
	return nil, nil
}

func (f *fakeStore) InsertEvalRun(context.Context, store.EvalRun) error { return nil }

func (f *fakeStore) ListEvalRuns(context.Context, store.EvalRunFilter) ([]store.EvalRun, error) {
	return nil, nil
}

func (f *fakeStore) GetEvalRun(context.Context, string) (store.EvalRun, error) {
	return store.EvalRun{}, store.ErrNotFound
}

func (f *fakeStore) Close() error { return nil }

func validSpan() span.Span {
	now := time.Now()
	return span.Span{
		TraceID:   "trace",
		SpanID:    "span",
		Name:      "call",
		Kind:      span.KindToolCall,
		StartTime: now,
		EndTime:   now.Add(time.Millisecond),
		Status:    span.StatusOK,
	}
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/spans", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func batchBody(t *testing.T, spans ...span.Span) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"spans": spans})
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	return string(b)
}

func TestAcceptsBatch(t *testing.T) {
	fs := &fakeStore{}
	rec := post(t, New(fs), batchBody(t, validSpan(), validSpan()))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body)
	}
	var resp response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Accepted != 2 || len(fs.spans) != 2 {
		t.Fatalf("accepted = %d, stored = %d, want 2/2", resp.Accepted, len(fs.spans))
	}
}

func TestRejectsInvalidJSON(t *testing.T) {
	rec := post(t, New(&fakeStore{}), `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRejectsEmptyBatch(t *testing.T) {
	rec := post(t, New(&fakeStore{}), `{"spans": []}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestRejectsInvalidSpan(t *testing.T) {
	bad := validSpan()
	bad.Name = ""
	rec := post(t, New(&fakeStore{}), batchBody(t, bad))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Fatalf("error body = %s", rec.Body)
	}
}

func TestCapsOversizedPayloads(t *testing.T) {
	fs := &fakeStore{}
	h := New(fs)
	h.maxPayload = 16

	s := validSpan()
	s.Input = json.RawMessage(`"` + strings.Repeat("x", 1000) + `"`)
	big := s.Input
	rec := post(t, h, batchBody(t, s))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body)
	}
	stored := fs.spans[0]
	if len(stored.Input) >= len(big) {
		t.Fatalf("payload not capped: %d bytes", len(stored.Input))
	}
	if stored.Attributes[span.AttrTruncated] != true {
		t.Fatalf("span not flagged as truncated: %v", stored.Attributes)
	}
	var parsed map[string]any
	if err := json.Unmarshal(stored.Input, &parsed); err != nil {
		t.Fatalf("capped payload is not valid JSON: %v", err)
	}
	if parsed["original_bytes"] != float64(len(big)) {
		t.Fatalf("original_bytes = %v, want %d", parsed["original_bytes"], len(big))
	}
}

func TestStoreFailureReturns500(t *testing.T) {
	rec := post(t, New(&fakeStore{err: errors.New("disk full")}), batchBody(t, validSpan()))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestRejectsOversizedBody(t *testing.T) {
	huge := strings.Repeat("x", maxBodyBytes+1)
	body := `{"spans": [{"trace_id": "` + huge + `"}]}`
	rec := post(t, New(&fakeStore{}), body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body)
	}
}
