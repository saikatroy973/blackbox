package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
)

func TestEvalRunsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	base := time.Now().UTC().Truncate(time.Microsecond)
	first := store.EvalRun{
		ID: "run-1", Suite: "alpha", StartedAt: base,
		DurationMS: 12, Total: 3, Passed: 2, Failed: 1, TotalCostUSD: 0.00042,
		Results: json.RawMessage(`[{"name":"a","passed":true}]`),
	}
	second := store.EvalRun{
		ID: "run-2", Suite: "beta", StartedAt: base.Add(time.Second),
		DurationMS: 5, Total: 1, Passed: 1,
	}
	for _, run := range []store.EvalRun{first, second} {
		if err := st.InsertEvalRun(ctx, run); err != nil {
			t.Fatalf("insert %s: %v", run.ID, err)
		}
	}

	all, err := st.ListEvalRuns(ctx, store.EvalRunFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %d, want 2", len(all))
	}
	if all[0].ID != "run-2" || all[1].ID != "run-1" {
		t.Fatalf("order = %s, %s; want run-2 first", all[0].ID, all[1].ID)
	}
	got := all[1]
	if got.Suite != "alpha" || got.Total != 3 || got.Passed != 2 || got.Failed != 1 {
		t.Fatalf("run-1 = %+v", got)
	}
	if got.TotalCostUSD != 0.00042 || got.DurationMS != 12 {
		t.Fatalf("run-1 metrics = %+v", got)
	}
	if !got.StartedAt.Equal(base) {
		t.Fatalf("started_at = %v, want %v", got.StartedAt, base)
	}

	var results []map[string]any
	if err := json.Unmarshal(got.Results, &results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(results) != 1 || results[0]["name"] != "a" {
		t.Fatalf("results = %+v", results)
	}
	if string(all[0].Results) != "[]" {
		t.Fatalf("empty results = %s, want []", all[0].Results)
	}

	alpha, err := st.ListEvalRuns(ctx, store.EvalRunFilter{Suite: "alpha"})
	if err != nil {
		t.Fatalf("list alpha: %v", err)
	}
	if len(alpha) != 1 || alpha[0].ID != "run-1" {
		t.Fatalf("alpha = %+v", alpha)
	}

	limited, err := st.ListEvalRuns(ctx, store.EvalRunFilter{Limit: 1})
	if err != nil {
		t.Fatalf("list limit: %v", err)
	}
	if len(limited) != 1 || limited[0].ID != "run-2" {
		t.Fatalf("limited = %+v", limited)
	}

	gotRun, err := st.GetEvalRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotRun.Suite != "alpha" || gotRun.Passed != 2 {
		t.Fatalf("get = %+v", gotRun)
	}
	if _, err := st.GetEvalRun(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing err = %v, want ErrNotFound", err)
	}
}
