package demoagent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/saikat/blackbox/internal/demoagent"
)

func runTool(t *testing.T, tool demoagent.Tool, args string) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), json.RawMessage(args))
}

func TestSearchToolMatchesCorpus(t *testing.T) {
	out, err := runTool(t, demoagent.SearchTool{}, `{"query": "What is the capital of France and its population?"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "Paris") {
		t.Fatalf("output = %q, want Paris", out)
	}
}

func TestSearchToolNoResults(t *testing.T) {
	out, err := runTool(t, demoagent.SearchTool{}, `{"query": "quantum bananas"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "No results found") {
		t.Fatalf("output = %q, want no results", out)
	}
}

func TestSearchToolErrors(t *testing.T) {
	if _, err := runTool(t, demoagent.SearchTool{}, `{"query": ""}`); err == nil {
		t.Fatal("empty query should error")
	}
	if _, err := runTool(t, demoagent.SearchTool{}, `not json`); err == nil {
		t.Fatal("invalid args should error")
	}
}

func TestCalculatorTool(t *testing.T) {
	cases := []struct {
		expression string
		want       string
	}{
		{"2 + 3", "2 + 3 = 5"},
		{"10 / 4", "10 / 4 = 2.5"},
		{"-2 * 3", "-2 * 3 = -6"},
		{"7 - 10", "7 - 10 = -3"},
	}
	for _, tc := range cases {
		args, _ := json.Marshal(map[string]string{"expression": tc.expression})
		out, err := runTool(t, demoagent.CalculatorTool{}, string(args))
		if err != nil {
			t.Fatalf("%q: %v", tc.expression, err)
		}
		if out != tc.want {
			t.Fatalf("%q = %q, want %q", tc.expression, out, tc.want)
		}
	}
}

func TestCalculatorToolErrors(t *testing.T) {
	cases := []string{
		`{"expression": "2 + 3 + 4"}`,
		`{"expression": "1 / 0"}`,
		`{"expression": "abc"}`,
		`not json`,
	}
	for _, args := range cases {
		if _, err := runTool(t, demoagent.CalculatorTool{}, args); err == nil {
			t.Fatalf("args %s should error", args)
		}
	}
}

func TestSummarizeTool(t *testing.T) {
	out, err := runTool(t, demoagent.SummarizeTool{},
		`{"text": "One. Two. Three.", "sentences": 2}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "One. Two." {
		t.Fatalf("output = %q, want %q", out, "One. Two.")
	}
}

func TestSummarizeToolErrors(t *testing.T) {
	if _, err := runTool(t, demoagent.SummarizeTool{}, `{"text": "  "}`); err == nil {
		t.Fatal("empty text should error")
	}
}

func TestRegistry(t *testing.T) {
	r := demoagent.DefaultRegistry()

	if _, ok := r.Get("web_search"); !ok {
		t.Fatal("web_search not registered")
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unexpected tool registered")
	}

	names := make([]string, 0, 3)
	for _, tool := range r.List() {
		names = append(names, tool.Name())
	}
	want := []string{"calculator", "summarize", "web_search"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}

	prompt := r.PromptList()
	for _, name := range want {
		if !strings.Contains(prompt, name) {
			t.Fatalf("prompt list missing %q: %s", name, prompt)
		}
	}
}
