package eval_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/saikat/blackbox/internal/eval"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "eval.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRunnerRecordsAndPersists(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	suite := eval.Suite{Name: "sample", Cases: []eval.Case{
		{
			Name:     "capital",
			Question: "What is the capital of France and its population?",
			Expect:   eval.Expect{OutputContains: []string{"Paris"}, ToolsCalled: []string{"web_search"}, MaxSteps: 3},
		},
		{
			Name:     "math",
			Question: "What is 2 + 3?",
			Expect:   eval.Expect{OutputContains: []string{"5"}, ToolsCalled: []string{"calculator"}},
		},
	}}

	res, err := eval.New(st, llm.NewMock(), "mock-1").Run(ctx, suite)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Total != 2 || res.Passed != 2 || res.Failed != 0 {
		t.Fatalf("run summary = %+v", res)
	}
	if res.TotalCostUSD <= 0 {
		t.Fatalf("total cost = %v, want > 0", res.TotalCostUSD)
	}

	seen := map[string]bool{}
	for _, c := range res.Cases {
		if !c.Passed || c.TraceID == "" || c.Steps != 2 {
			t.Fatalf("case = %+v", c)
		}
		if seen[c.TraceID] {
			t.Fatalf("duplicate trace id %s", c.TraceID)
		}
		seen[c.TraceID] = true
		if c.CostUSD <= 0 {
			t.Fatalf("case cost = %v, want > 0", c.CostUSD)
		}
	}

	// Every case trace is tagged so eval runs are findable in the dashboard.
	spans, err := st.GetTrace(ctx, res.Cases[0].TraceID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	var rootAttrs map[string]any
	for _, s := range spans {
		if s.ParentSpanID == "" {
			rootAttrs = s.Attributes
		}
	}
	if rootAttrs["blackbox.eval_suite"] != "sample" || rootAttrs["blackbox.eval_case"] != "capital" {
		t.Fatalf("root attributes = %v", rootAttrs)
	}

	// The suite run is persisted with per-case results.
	runs, err := st.ListEvalRuns(ctx, store.EvalRunFilter{Suite: "sample"})
	if err != nil {
		t.Fatalf("list eval runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	got := runs[0]
	if got.ID != res.ID || got.Total != 2 || got.Passed != 2 || got.Failed != 0 {
		t.Fatalf("persisted run = %+v", got)
	}
	var cases []eval.CaseResult
	if err := json.Unmarshal(got.Results, &cases); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(cases) != 2 || cases[0].Name != "capital" {
		t.Fatalf("persisted cases = %+v", cases)
	}
}

func TestRunnerFailingCase(t *testing.T) {
	st := openStore(t)
	suite := eval.Suite{Name: "failing", Cases: []eval.Case{{
		Name:     "impossible",
		Question: "What is 2 + 3?",
		Expect:   eval.Expect{OutputContains: []string{"definitely-not-there"}, MaxSteps: 1},
	}}}

	res, err := eval.New(st, llm.NewMock(), "mock-1").Run(context.Background(), suite)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Passed != 0 || res.Failed != 1 {
		t.Fatalf("summary = %+v", res)
	}
	c := res.Cases[0]
	if c.Passed || len(c.Failures) != 2 {
		t.Fatalf("case = %+v", c)
	}
	if c.Answer == "" {
		t.Fatal("expected the answer to be captured for failed cases")
	}
}

func TestCompare(t *testing.T) {
	baseline := eval.RunResult{Suite: "s", Cases: []eval.CaseResult{
		{Name: "a", Passed: true},
		{Name: "b", Passed: true},
		{Name: "c", Passed: false, Failures: []string{"was already failing"}},
	}}
	current := eval.RunResult{Suite: "s", Cases: []eval.CaseResult{
		{Name: "a", Passed: false, Failures: []string{"output does not contain \"x\""}},
		{Name: "b", Passed: true},
		{Name: "c", Passed: false, Failures: []string{"still failing"}},
		{Name: "d", Passed: false, Failures: []string{"new case"}},
	}}

	regs := eval.Compare(baseline, current)
	if len(regs) != 1 || regs[0].Case != "a" {
		t.Fatalf("regressions = %+v, want only case a", regs)
	}
	if regs[0].Reason == "" {
		t.Fatal("regression reason is empty")
	}
}
