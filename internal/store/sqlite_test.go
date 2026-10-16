package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func open(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return st
}

func base() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func sampleSpans() []span.Span {
	b := base()
	return []span.Span{
		{
			TraceID:    "trace-1",
			SpanID:     "root",
			Name:       "agent",
			Kind:       span.KindAgentRun,
			StartTime:  b,
			EndTime:    b.Add(2 * time.Second),
			Status:     span.StatusOK,
			Attributes: map[string]any{"service.name": "demo"},
		},
		{
			TraceID:      "trace-1",
			SpanID:       "llm",
			ParentSpanID: "root",
			Name:         "plan",
			Kind:         span.KindLLMCall,
			StartTime:    b.Add(100 * time.Millisecond),
			EndTime:      b.Add(500 * time.Millisecond),
			Status:       span.StatusError,
			Error:        "boom",
			Attributes:   map[string]any{span.AttrModel: "mock-1"},
			Input:        json.RawMessage(`"q"`),
			Output:       json.RawMessage(`"a"`),
		},
	}
}

func TestInsertAndGetTrace(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	if err := st.InsertSpans(ctx, sampleSpans()); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := st.GetTrace(ctx, "trace-1")
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2", len(got))
	}

	root, llm := got[0], got[1]
	if root.SpanID != "root" || llm.SpanID != "llm" {
		t.Fatalf("spans not ordered by start time: %s, %s", root.SpanID, llm.SpanID)
	}
	if root.Kind != span.KindAgentRun || llm.Kind != span.KindLLMCall {
		t.Fatalf("kinds not round-tripped: %s, %s", root.Kind, llm.Kind)
	}
	if !root.StartTime.Equal(base()) || !root.EndTime.Equal(base().Add(2*time.Second)) {
		t.Fatalf("root times not round-tripped: %v - %v", root.StartTime, root.EndTime)
	}
	if !reflect.DeepEqual(root.Attributes, map[string]any{"service.name": "demo"}) {
		t.Fatalf("root attributes = %v", root.Attributes)
	}
	if llm.Status != span.StatusError || llm.Error != "boom" {
		t.Fatalf("llm status/error = %q/%q", llm.Status, llm.Error)
	}
	if string(llm.Input) != `"q"` || string(llm.Output) != `"a"` {
		t.Fatalf("payloads = %s / %s", llm.Input, llm.Output)
	}
	if got := llm.Attributes[span.AttrModel]; got != "mock-1" {
		t.Fatalf("llm model attr = %v", got)
	}
}

func TestGetTraceNotFound(t *testing.T) {
	st := open(t)
	_, err := st.GetTrace(context.Background(), "missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTrace() = %v, want ErrNotFound", err)
	}
}

func TestInsertSpansEmptyIsNoop(t *testing.T) {
	st := open(t)
	if err := st.InsertSpans(context.Background(), nil); err != nil {
		t.Fatalf("insert nil: %v", err)
	}
}

func TestListTraces(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	spans := sampleSpans()
	second := spans[0]
	second.TraceID = "trace-2"
	second.SpanID = "root-2"
	second.Name = "agent-2"
	second.StartTime = second.StartTime.Add(time.Hour)
	second.EndTime = second.EndTime.Add(time.Hour)
	spans = append(spans, second)

	if err := st.InsertSpans(ctx, spans); err != nil {
		t.Fatalf("insert: %v", err)
	}

	sums, err := st.ListTraces(ctx, store.TraceFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("got %d summaries, want 2", len(sums))
	}

	if sums[0].TraceID != "trace-2" {
		t.Fatalf("first summary = %q, want most recent trace-2", sums[0].TraceID)
	}
	if sums[0].RootName != "agent-2" || sums[0].SpanCount != 1 || sums[0].ErrorCount != 0 {
		t.Fatalf("trace-2 summary = %+v", sums[0])
	}

	first := sums[1]
	if first.TraceID != "trace-1" || first.RootName != "agent" {
		t.Fatalf("trace-1 summary = %+v", first)
	}
	if first.SpanCount != 2 || first.ErrorCount != 1 {
		t.Fatalf("trace-1 counts = %d spans / %d errors, want 2/1", first.SpanCount, first.ErrorCount)
	}
	if !first.StartTime.Equal(base()) || !first.EndTime.Equal(base().Add(2*time.Second)) {
		t.Fatalf("trace-1 times = %v - %v", first.StartTime, first.EndTime)
	}
}

func TestListTracesLimit(t *testing.T) {
	st := open(t)
	ctx := context.Background()

	spans := sampleSpans()
	second := spans[0]
	second.TraceID = "trace-2"
	second.SpanID = "root-2"
	second.StartTime = second.StartTime.Add(time.Hour)
	second.EndTime = second.EndTime.Add(time.Hour)
	spans = append(spans, second)

	if err := st.InsertSpans(ctx, spans); err != nil {
		t.Fatalf("insert: %v", err)
	}
	sums, err := st.ListTraces(ctx, store.TraceFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sums) != 1 || sums[0].TraceID != "trace-2" {
		t.Fatalf("limit not applied: %+v", sums)
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	st1, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st2, err := store.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if err := st2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
