package eval_test

import (
	"strings"
	"testing"

	"github.com/saikat/blackbox/internal/eval"
)

func TestCheck(t *testing.T) {
	result := eval.CaseResult{
		Answer:     "The answer is 56088.",
		Tools:      []string{"calculator", "web_search"},
		Steps:      2,
		CostUSD:    0.0001,
		DurationMS: 5,
	}

	if failures := eval.Check(result, eval.Expect{
		OutputContains:    []string{"56088"},
		OutputNotContains: []string{"Paris"},
		ToolsCalled:       []string{"calculator"},
		MaxSteps:          3,
		MaxCostUSD:        0.001,
		MaxDurationMS:     100,
	}); len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}

	failures := eval.Check(result, eval.Expect{
		OutputContains:    []string{"Paris"},
		OutputNotContains: []string{"56088"},
		ToolsCalled:       []string{"summarize"},
		MaxSteps:          1,
		MaxCostUSD:        0.00001,
		MaxDurationMS:     1,
	})
	joined := strings.Join(failures, "\n")
	for _, want := range []string{
		`does not contain "Paris"`,
		`contains "56088"`,
		`tool "summarize" was not called`,
		"took 2 steps, limit is 1",
		"exceeds limit",
		"took 5ms, limit is 1ms",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("failures missing %q:\n%s", want, joined)
		}
	}
	if len(failures) != 6 {
		t.Fatalf("failures = %d, want 6:\n%s", len(failures), joined)
	}
}
