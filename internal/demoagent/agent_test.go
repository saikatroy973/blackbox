package demoagent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

type collector struct {
	mu    sync.Mutex
	spans []span.Span
}

func (c *collector) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Spans []span.Span `json:"spans"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.spans = append(c.spans, req.Spans...)
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}
}

func (c *collector) drained() []span.Span {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.spans
	c.spans = nil
	return out
}

func newAgent(t *testing.T, provider llm.Provider, maxSteps int) (*demoagent.Agent, *collector, *tracer.Tracer) {
	t.Helper()
	c := &collector{}
	srv := httptest.NewServer(c.handler())
	t.Cleanup(srv.Close)

	tr := tracer.New(tracer.Config{
		Endpoint:      srv.URL,
		ServiceName:   "demoagent",
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	t.Cleanup(func() {
		if err := tr.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return demoagent.New(demoagent.Config{Provider: provider, Tracer: tr, Model: "mock-1", MaxSteps: maxSteps}), c, tr
}

func spansOfKind(spans []span.Span, kind span.Kind) []span.Span {
	var out []span.Span
	for _, s := range spans {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

type stubProvider struct{ reply string }

func (s stubProvider) Name() string { return "stub" }

func (s stubProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Content: s.reply}, nil
}

func TestRunAnswersWithSearch(t *testing.T) {
	agent, c, tr := newAgent(t, llm.NewMock(), 0)

	res, err := agent.Run(context.Background(), "What is the capital of France and its population?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Answer, "Paris") {
		t.Fatalf("answer = %q, want it to mention Paris", res.Answer)
	}
	if res.Steps != 2 {
		t.Fatalf("steps = %d, want 2", res.Steps)
	}
	if res.TraceID == "" {
		t.Fatal("trace ID is empty")
	}

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	spans := c.drained()

	if got := len(spansOfKind(spans, span.KindAgentRun)); got != 1 {
		t.Fatalf("agent.run spans = %d, want 1", got)
	}
	if got := len(spansOfKind(spans, span.KindAgentStep)); got != 2 {
		t.Fatalf("agent.step spans = %d, want 2", got)
	}
	if got := len(spansOfKind(spans, span.KindLLMCall)); got != 2 {
		t.Fatalf("llm.call spans = %d, want 2", got)
	}
	tools := spansOfKind(spans, span.KindToolCall)
	if len(tools) != 1 || tools[0].Name != "web_search" {
		t.Fatalf("tool spans = %+v, want one web_search", tools)
	}
	if tools[0].Status != span.StatusOK {
		t.Fatalf("tool status = %q", tools[0].Status)
	}
	if !strings.Contains(string(tools[0].Input), "France") {
		t.Fatalf("tool input = %s, want the query", tools[0].Input)
	}
	if !strings.Contains(string(tools[0].Output), "Paris") {
		t.Fatalf("tool output = %s, want search result", tools[0].Output)
	}

	var run span.Span
	for _, s := range spans {
		if s.Kind == span.KindAgentRun {
			run = s
		}
	}
	if run.TraceID != res.TraceID {
		t.Fatalf("run trace = %q, want %q", run.TraceID, res.TraceID)
	}
	if run.Attributes[span.AttrAgentName] != "research-agent" {
		t.Fatalf("agent name attr = %v", run.Attributes[span.AttrAgentName])
	}
	if !strings.Contains(string(run.Output), "Paris") {
		t.Fatalf("run output = %s, want the answer", run.Output)
	}

	for _, s := range spansOfKind(spans, span.KindAgentStep) {
		if s.ParentSpanID != run.SpanID {
			t.Fatalf("step %q parent = %q, want %q", s.Name, s.ParentSpanID, run.SpanID)
		}
	}
	for _, s := range spansOfKind(spans, span.KindLLMCall) {
		if s.Attributes[span.AttrModel] != "mock-1" {
			t.Fatalf("llm model attr = %v", s.Attributes[span.AttrModel])
		}
		if s.Attributes[span.AttrCostUSD] == nil {
			t.Fatal("llm span missing cost attribute")
		}
	}
}

func TestRunAnswersWithCalculator(t *testing.T) {
	agent, c, tr := newAgent(t, llm.NewMock(), 0)

	res, err := agent.Run(context.Background(), "What is 123 * 456?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Answer, "56088") {
		t.Fatalf("answer = %q, want 56088", res.Answer)
	}

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	tools := spansOfKind(c.drained(), span.KindToolCall)
	if len(tools) != 1 || tools[0].Name != "calculator" {
		t.Fatalf("tool spans = %+v, want one calculator", tools)
	}
}

func TestUnknownToolFailsRun(t *testing.T) {
	agent, c, tr := newAgent(t, stubProvider{reply: `{"tool": "nope", "args": {}}`}, 0)

	_, err := agent.Run(context.Background(), "anything")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("err = %v, want unknown tool", err)
	}

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	spans := c.drained()

	runs := spansOfKind(spans, span.KindAgentRun)
	if len(runs) != 1 || runs[0].Status != span.StatusError {
		t.Fatalf("run spans = %+v, want one error status", runs)
	}
	tools := spansOfKind(spans, span.KindToolCall)
	if len(tools) != 1 || tools[0].Status != span.StatusError {
		t.Fatalf("tool spans = %+v, want one error status", tools)
	}
}

func TestMaxStepsExceeded(t *testing.T) {
	agent, _, _ := newAgent(t, stubProvider{reply: `{"tool": "web_search", "args": {"query": "loop"}}`}, 3)

	res, err := agent.Run(context.Background(), "anything")
	if err == nil || !strings.Contains(err.Error(), "exceeded max steps") {
		t.Fatalf("err = %v, want max steps", err)
	}
	if res.Steps != 3 {
		t.Fatalf("steps = %d, want 3", res.Steps)
	}
}

func TestInvalidActionJSONFails(t *testing.T) {
	agent, _, _ := newAgent(t, stubProvider{reply: "I think the answer is 42."}, 0)

	_, err := agent.Run(context.Background(), "anything")
	if err == nil || !strings.Contains(err.Error(), "invalid action JSON") {
		t.Fatalf("err = %v, want invalid action JSON", err)
	}
}
