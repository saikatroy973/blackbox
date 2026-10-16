package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

const benchBatchSize = 50

func benchBody(b *testing.B, n int) []byte {
	b.Helper()
	now := time.Now()
	spans := make([]span.Span, n)
	for i := range spans {
		spans[i] = span.Span{
			TraceID:   "bench-trace",
			SpanID:    fmt.Sprintf("s%04d", i),
			Name:      "step",
			Kind:      span.KindAgentStep,
			StartTime: now,
			EndTime:   now.Add(time.Millisecond),
			Status:    span.StatusOK,
			Input:     json.RawMessage(`{"question":"what is the capital of france"}`),
		}
	}
	body, err := json.Marshal(map[string]any{"spans": spans})
	if err != nil {
		b.Fatalf("marshal: %v", err)
	}
	return body
}

// BenchmarkIngestValidate measures the HTTP path: decode, cap, validate,
// notify, with storage stubbed out.
func BenchmarkIngestValidate(b *testing.B) {
	h := New(&fakeStore{})
	body := benchBody(b, benchBatchSize)

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/spans", bytes.NewReader(body))
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			b.Fatalf("status = %d", rec.Code)
		}
	}
	b.ReportMetric(float64(benchBatchSize*b.N)/b.Elapsed().Seconds(), "spans/s")
}

// BenchmarkIngestSQLite measures the full path including a batched SQLite
// transaction.
func BenchmarkIngestSQLite(b *testing.B) {
	st, err := store.OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	h := New(st)
	body := benchBody(b, benchBatchSize)

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/spans", bytes.NewReader(body))
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			b.Fatalf("status = %d", rec.Code)
		}
	}
	b.ReportMetric(float64(benchBatchSize*b.N)/b.Elapsed().Seconds(), "spans/s")
}
