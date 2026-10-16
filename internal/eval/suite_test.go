package eval_test

import (
	"strings"
	"testing"

	"github.com/saikat/blackbox/internal/eval"
)

func TestParseSuiteValid(t *testing.T) {
	const doc = `
suite: sample
cases:
  - name: a
    question: "q1"
    expect:
      output_contains: ["x"]
      tools_called: ["web_search"]
      max_steps: 3
      max_cost_usd: 0.5
  - name: b
    question: "q2"
`
	s, err := eval.ParseSuite(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ParseSuite: %v", err)
	}
	if s.Name != "sample" || len(s.Cases) != 2 {
		t.Fatalf("suite = %+v", s)
	}
	a := s.Cases[0]
	if a.Expect.MaxSteps != 3 || a.Expect.MaxCostUSD != 0.5 || len(a.Expect.OutputContains) != 1 {
		t.Fatalf("case a = %+v", a)
	}
}

func TestParseSuiteErrors(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"missing name", "cases:\n  - name: a\n    question: q\n", "missing a name"},
		{"no cases", "suite: s\n", "no cases"},
		{"missing case name", "suite: s\ncases:\n  - question: q\n", "missing a name"},
		{"missing question", "suite: s\ncases:\n  - name: a\n", "missing a question"},
		{"duplicate", "suite: s\ncases:\n  - name: a\n    question: q\n  - name: a\n    question: q\n", "duplicate"},
		{"unknown field", "suite: s\ncases:\n  - name: a\n    question: q\n    bogus: 1\n", "field bogus"},
		{"bad yaml", "suite: [", "decode suite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := eval.ParseSuite(strings.NewReader(tt.doc))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}
