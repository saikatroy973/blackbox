package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func uniquePrefix(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return t.Name() + "-" + hex.EncodeToString(b[:])
}

func indexOfTrace(sums []store.TraceSummary, id string) int {
	for i, s := range sums {
		if s.TraceID == id {
			return i
		}
	}
	return -1
}

// runStoreConformance checks the behavioral contract every Store
// implementation must satisfy.
func runStoreConformance(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	prefix := uniquePrefix(t)
	t1, t2 := prefix+"-t1", prefix+"-t2"
	base := time.Now().UTC().Truncate(time.Microsecond)

	spans := []span.Span{
		{
			TraceID: t1, SpanID: "s1", Name: "agent", Kind: span.KindAgentRun,
			StartTime: base, EndTime: base.Add(2 * time.Second), Status: span.StatusOK,
			Attributes: map[string]any{span.AttrAgentName: "agent", span.AttrCostUSD: 0.0004},
			Output:     json.RawMessage(`{"answer":"Paris"}`),
		},
		{
			TraceID: t1, SpanID: "s2", ParentSpanID: "s1", Name: "search", Kind: span.KindToolCall,
			StartTime: base.Add(100 * time.Millisecond), EndTime: base.Add(400 * time.Millisecond),
			Status: span.StatusError, Error: "rate limited",
			Input: json.RawMessage(`{"query":"x"}`),
		},
		{
			TraceID: t2, SpanID: "s3", Name: "other", Kind: span.KindAgentRun,
			StartTime: base.Add(time.Minute), EndTime: base.Add(time.Minute + time.Second),
			Status: span.StatusOK,
		},
	}
	if err := st.InsertSpans(ctx, spans); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := st.GetTrace(ctx, t1)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(got) != 2 || got[0].SpanID != "s1" || got[1].SpanID != "s2" {
		t.Fatalf("spans = %+v", got)
	}
	root := got[0]
	if root.Attributes[span.AttrAgentName] != "agent" {
		t.Fatalf("string attribute lost: %+v", root.Attributes)
	}
	if cost, ok := root.Attributes[span.AttrCostUSD].(float64); !ok || cost != 0.0004 {
		t.Fatalf("float attribute lost: %+v", root.Attributes)
	}
	if string(root.Output) != `{"answer":"Paris"}` {
		t.Fatalf("output = %s", root.Output)
	}
	web := got[1]
	if web.Status != span.StatusError || web.Error != "rate limited" || string(web.Input) != `{"query":"x"}` {
		t.Fatalf("error span = %+v", web)
	}

	spans[0].Output = json.RawMessage(`{"answer":"Lyon"}`)
	if err := st.InsertSpans(ctx, spans[:1]); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	got, err = st.GetTrace(ctx, t1)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if len(got) != 2 || string(got[0].Output) != `{"answer":"Lyon"}` {
		t.Fatalf("upsert did not overwrite: %+v", got)
	}

	if _, err := st.GetTrace(ctx, prefix+"-missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing trace err = %v, want ErrNotFound", err)
	}

	sums, err := st.ListTraces(ctx, store.TraceFilter{Limit: 500})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pos1, pos2 := indexOfTrace(sums, t1), indexOfTrace(sums, t2)
	if pos1 < 0 || pos2 < 0 {
		t.Fatalf("traces missing from list: %v", sums)
	}
	if pos2 > pos1 {
		t.Fatalf("expected the newer trace (%s) before the older one", t2)
	}
	summary := sums[pos1]
	if summary.RootName != "agent" || summary.SpanCount != 2 || summary.ErrorCount != 1 {
		t.Fatalf("summary = %+v", summary)
	}

	errs, err := st.ListTraces(ctx, store.TraceFilter{Status: "error", Limit: 500})
	if err != nil {
		t.Fatalf("list errors: %v", err)
	}
	if indexOfTrace(errs, t1) < 0 || indexOfTrace(errs, t2) >= 0 {
		t.Fatalf("error filter wrong: %v", errs)
	}
	oks, err := st.ListTraces(ctx, store.TraceFilter{Status: "ok", Limit: 500})
	if err != nil {
		t.Fatalf("list ok: %v", err)
	}
	if indexOfTrace(oks, t2) < 0 || indexOfTrace(oks, t1) >= 0 {
		t.Fatalf("ok filter wrong: %v", oks)
	}

	limited, err := st.ListTraces(ctx, store.TraceFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list limit: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limit ignored: %v", limited)
	}

	runA := store.EvalRun{
		ID: prefix + "-r1", Suite: prefix, StartedAt: base,
		DurationMS: 5, Total: 2, Passed: 1, Failed: 1, TotalCostUSD: 0.0002,
		Results: json.RawMessage(`[{"name":"a"}]`),
	}
	runB := store.EvalRun{
		ID: prefix + "-r2", Suite: prefix, StartedAt: base.Add(time.Second),
		DurationMS: 3, Total: 1, Passed: 1,
	}
	for _, run := range []store.EvalRun{runA, runB} {
		if err := st.InsertEvalRun(ctx, run); err != nil {
			t.Fatalf("insert eval run %s: %v", run.ID, err)
		}
	}
	runs, err := st.ListEvalRuns(ctx, store.EvalRunFilter{Suite: prefix})
	if err != nil {
		t.Fatalf("list eval runs: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != runB.ID || runs[1].ID != runA.ID {
		t.Fatalf("eval runs = %+v", runs)
	}
	if string(runs[0].Results) != "[]" {
		t.Fatalf("empty results = %s, want []", runs[0].Results)
	}
	gotRun, err := st.GetEvalRun(ctx, runA.ID)
	if err != nil {
		t.Fatalf("get eval run: %v", err)
	}
	if gotRun.Suite != prefix || gotRun.Total != 2 || gotRun.Passed != 1 || !gotRun.StartedAt.Equal(base) {
		t.Fatalf("eval run = %+v", gotRun)
	}
	if _, err := st.GetEvalRun(ctx, prefix+"-missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing eval run err = %v, want ErrNotFound", err)
	}
}

func TestSQLiteConformance(t *testing.T) {
	runStoreConformance(t, open(t))
}
