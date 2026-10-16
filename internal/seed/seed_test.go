package seed_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/saikat/blackbox/internal/seed"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func open(t *testing.T) store.Store {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func writeSuite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	doc := `suite: seed-check
cases:
  - name: math
    question: "What is 2 + 3?"
    expect:
      output_contains: ["5"]
`
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return dir
}

func TestRunSeedsTracesReplayAndEvals(t *testing.T) {
	ctx := context.Background()
	st := open(t)

	report, err := seed.Run(ctx, st, seed.Options{Runs: 2, EvalsDir: writeSuite(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Traces) != 2 || report.ErrorTrace == "" || report.ReplayTrace == "" {
		t.Fatalf("report = %+v", report)
	}
	if report.EvalSuites != 1 || report.EvalCases != 1 {
		t.Fatalf("eval report = %+v", report)
	}

	sums, err := st.ListTraces(ctx, store.TraceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sums) != 5 { // 2 runs + error trace + replay + 1 eval case trace
		t.Fatalf("traces = %d, want 5", len(sums))
	}

	errSpans, err := st.GetTrace(ctx, report.ErrorTrace)
	if err != nil {
		t.Fatalf("get error trace: %v", err)
	}
	var sawToolError bool
	for _, s := range errSpans {
		if s.Kind == span.KindToolCall && s.Status == span.StatusError && s.Error != "" {
			sawToolError = true
		}
	}
	if !sawToolError {
		t.Fatal("error trace has no failing tool span")
	}

	replaySpans, err := st.GetTrace(ctx, report.ReplayTrace)
	if err != nil {
		t.Fatalf("get replay trace: %v", err)
	}
	if got := replaySpans[0].Attributes[span.AttrReplayOf]; got != report.Traces[0] {
		t.Fatalf("replay_of = %v, want %q", got, report.Traces[0])
	}

	runs, err := st.ListEvalRuns(ctx, store.EvalRunFilter{})
	if err != nil {
		t.Fatalf("list eval runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Suite != "seed-check" || runs[0].Passed != 1 {
		t.Fatalf("eval runs = %+v", runs)
	}
}

func TestRunWithoutEvalsDir(t *testing.T) {
	report, err := seed.Run(context.Background(), open(t), seed.Options{Runs: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.EvalSuites != 0 || report.EvalCases != 0 {
		t.Fatalf("eval report = %+v", report)
	}
	if len(report.Traces) != 1 {
		t.Fatalf("traces = %+v", report.Traces)
	}
}
