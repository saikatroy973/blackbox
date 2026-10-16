// Package demoagent implements Blackbox's demo workload: a small ReAct
// research agent instrumented with the tracer SDK.
package demoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tool is a callable capability the agent can use.
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry holds the agent's tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns a Registry containing tools.
func NewRegistry(tools ...Tool) *Registry {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Registry{tools: m}
}

// DefaultRegistry returns the demo tool set.
func DefaultRegistry() *Registry {
	return NewRegistry(SearchTool{}, CalculatorTool{}, SummarizeTool{})
}

// Get returns the tool with the given name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// List returns the tools sorted by name.
func (r *Registry) List() []Tool {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// PromptList renders the tools for the agent system prompt.
func (r *Registry) PromptList() string {
	var b strings.Builder
	for _, t := range r.List() {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name(), t.Description())
	}
	return b.String()
}

// SearchTool is a deterministic stand-in for a web search API.
type SearchTool struct{}

// corpus maps topic keys to canned results. Keys are matched against the
// query as case-insensitive substrings, in sorted order.
var corpus = map[string]string{
	"capital of france": "Paris is the capital of France. City population: about 2.1 million; metro area: about 12.2 million (2024 estimate).",
	"go language":       "Go is a statically typed, compiled programming language designed at Google. It is known for simple syntax, fast builds, and first-class concurrency with goroutines and channels.",
	"llm agents":        "LLM agents pair a language model with tools, memory, and a control loop. Common patterns are ReAct (reason + act), planning, and reflection. Per-step observability is critical in production.",
}

// Name implements Tool.
func (SearchTool) Name() string { return "web_search" }

// Description implements Tool.
func (SearchTool) Description() string {
	return `Search a small mock web corpus. Args: {"query": "<text>"}.`
}

// Run implements Tool.
func (SearchTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("web_search: invalid args: %w", err)
	}
	q := strings.ToLower(strings.TrimSpace(a.Query))
	if q == "" {
		return "", fmt.Errorf("web_search: query is required")
	}

	keys := make([]string, 0, len(corpus))
	for k := range corpus {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(q, k) || strings.Contains(k, q) {
			return corpus[k], nil
		}
	}
	return fmt.Sprintf("No results found for %q.", a.Query), nil
}

var expressionRe = regexp.MustCompile(`^\s*(-?\d+(?:\.\d+)?)\s*([+\-*/])\s*(-?\d+(?:\.\d+)?)\s*$`)

// CalculatorTool evaluates single arithmetic expressions.
type CalculatorTool struct{}

// Name implements Tool.
func (CalculatorTool) Name() string { return "calculator" }

// Description implements Tool.
func (CalculatorTool) Description() string {
	return `Evaluate one arithmetic expression. Args: {"expression": "12 * 7"}.`
}

// Run implements Tool.
func (CalculatorTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("calculator: invalid args: %w", err)
	}
	m := expressionRe.FindStringSubmatch(a.Expression)
	if m == nil {
		return "", fmt.Errorf("calculator: unsupported expression %q", a.Expression)
	}
	x, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return "", fmt.Errorf("calculator: parse operand: %w", err)
	}
	y, err := strconv.ParseFloat(m[3], 64)
	if err != nil {
		return "", fmt.Errorf("calculator: parse operand: %w", err)
	}

	var result float64
	switch m[2] {
	case "+":
		result = x + y
	case "-":
		result = x - y
	case "*":
		result = x * y
	case "/":
		if y == 0 {
			return "", fmt.Errorf("calculator: division by zero")
		}
		result = x / y
	}
	return fmt.Sprintf("%s = %s", strings.TrimSpace(a.Expression), strconv.FormatFloat(result, 'f', -1, 64)), nil
}

// SummarizeTool shortens text to its first sentences.
type SummarizeTool struct{}

// Name implements Tool.
func (SummarizeTool) Name() string { return "summarize" }

// Description implements Tool.
func (SummarizeTool) Description() string {
	return `Summarize text by keeping its first sentences. Args: {"text": "<text>", "sentences": 2}.`
}

// Run implements Tool.
func (SummarizeTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Text      string `json:"text"`
		Sentences int    `json:"sentences"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("summarize: invalid args: %w", err)
	}
	if strings.TrimSpace(a.Text) == "" {
		return "", fmt.Errorf("summarize: text is required")
	}
	n := a.Sentences
	if n <= 0 {
		n = 2
	}

	sentences := splitSentences(a.Text)
	if len(sentences) > n {
		sentences = sentences[:n]
	}
	return strings.Join(sentences, " "), nil
}

func splitSentences(text string) []string {
	parts := strings.Split(strings.TrimSpace(text), ". ")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if i < len(parts)-1 && !strings.HasSuffix(p, ".") {
			p += "."
		}
		out = append(out, p)
	}
	return out
}
