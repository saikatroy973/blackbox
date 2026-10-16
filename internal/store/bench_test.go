package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func benchSpans(traceID string, n int) []span.Span {
	now := time.Now()
	spans := make([]span.Span, n)
	for i := range spans {
		parent := ""
		if i > 0 {
			parent = "s0"
		}
		spans[i] = span.Span{
			TraceID: traceID, SpanID: fmt.Sprintf("s%04d", i), ParentSpanID: parent,
			Name: "step", Kind: span.KindAgentStep,
			StartTime: now.Add(time.Duration(i) * time.Millisecond),
			EndTime:   now.Add(time.Duration(i+1) * time.Millisecond),
			Status:    span.StatusOK,
			Attributes: map[string]any{
				span.AttrInputTokens:  100,
				span.AttrOutputTokens: 50,
				span.AttrModel:        "mock-1",
			},
			Input:  json.RawMessage(`{"question":"what is the capital of france"}`),
			Output: json.RawMessage(`{"tool":"web_search"}`),
		}
	}
	return spans
}

func BenchmarkInsertSpansSQLite(b *testing.B) {
	st, err := store.OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	spans := benchSpans("bench-insert", 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := st.InsertSpans(context.Background(), spans); err != nil {
			b.Fatalf("insert: %v", err)
		}
	}
	b.ReportMetric(float64(len(spans)*b.N)/b.Elapsed().Seconds(), "spans/s")
}

func BenchmarkGetTraceSQLite(b *testing.B) {
	st, err := store.OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.InsertSpans(context.Background(), benchSpans("bench-get", 20)); err != nil {
		b.Fatalf("seed: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.GetTrace(context.Background(), "bench-get"); err != nil {
			b.Fatalf("get: %v", err)
		}
	}
}

func BenchmarkListTracesSQLite(b *testing.B) {
	st, err := store.OpenSQLite(context.Background(), ":memory:")
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := st.InsertSpans(ctx, benchSpans(fmt.Sprintf("bench-list-%03d", i), 10)); err != nil {
			b.Fatalf("seed %d: %v", i, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.ListTraces(ctx, store.TraceFilter{Limit: 50}); err != nil {
			b.Fatalf("list: %v", err)
		}
	}
}
