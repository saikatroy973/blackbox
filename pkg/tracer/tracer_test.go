package tracer_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

func newTestTracer(t *testing.T, c *collector, cfg tracer.Config) (*tracer.Tracer, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(c.handler())
	t.Cleanup(srv.Close)
	cfg.Endpoint = srv.URL
	tr := tracer.New(cfg)
	t.Cleanup(func() {
		if err := tr.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return tr, srv
}

func TestSpanHierarchy(t *testing.T) {
	c := &collector{}
	tr, _ := newTestTracer(t, c, tracer.Config{BatchSize: 1, FlushInterval: time.Hour})

	ctx, run := tr.StartRun(context.Background(), "research-agent")
	_, llm := tr.StartLLMCall(ctx, "plan")
	llm.SetAttribute(span.AttrModel, "mock-1")
	if err := llm.SetInput("question"); err != nil {
		t.Fatalf("SetInput: %v", err)
	}
	if err := llm.SetOutput("thought"); err != nil {
		t.Fatalf("SetOutput: %v", err)
	}
	llm.End()
	_, tool := tr.StartToolCall(ctx, "web_search")
	tool.End()
	run.End()

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	spans := c.drained()
	if len(spans) != 3 {
		t.Fatalf("exported %d spans, want 3", len(spans))
	}
	byName := make(map[string]span.Span, len(spans))
	for _, s := range spans {
		byName[s.Name] = s
	}

	runSpan := byName["research-agent"]
	llmSpan := byName["plan"]
	toolSpan := byName["web_search"]

	if runSpan.Kind != span.KindAgentRun || runSpan.ParentSpanID != "" {
		t.Fatalf("root span = %+v, want agent.run with no parent", runSpan)
	}
	if runSpan.TraceID == "" || runSpan.SpanID == "" {
		t.Fatalf("root span missing ids: %+v", runSpan)
	}
	for _, s := range []span.Span{llmSpan, toolSpan} {
		if s.TraceID != runSpan.TraceID {
			t.Fatalf("span %q trace = %q, want %q", s.Name, s.TraceID, runSpan.TraceID)
		}
		if s.ParentSpanID != runSpan.SpanID {
			t.Fatalf("span %q parent = %q, want %q", s.Name, s.ParentSpanID, runSpan.SpanID)
		}
		if s.Status != span.StatusOK {
			t.Fatalf("span %q status = %q, want ok", s.Name, s.Status)
		}
	}
	if llmSpan.Kind != span.KindLLMCall {
		t.Fatalf("llm span kind = %q, want llm.call", llmSpan.Kind)
	}
	if toolSpan.Kind != span.KindToolCall {
		t.Fatalf("tool span kind = %q, want tool.call", toolSpan.Kind)
	}
	if got := llmSpan.Attributes[span.AttrModel]; got != "mock-1" {
		t.Fatalf("model attribute = %v, want mock-1", got)
	}
	if string(llmSpan.Input) != `"question"` {
		t.Fatalf("input = %s, want %q", llmSpan.Input, "question")
	}
	if !llmSpan.EndTime.After(llmSpan.StartTime) {
		t.Fatalf("end time %v not after start %v", llmSpan.EndTime, llmSpan.StartTime)
	}
}

func TestShutdownFlushesQueue(t *testing.T) {
	c := &collector{}
	tr, _ := newTestTracer(t, c, tracer.Config{BatchSize: 1000, FlushInterval: time.Hour})

	_, s1 := tr.StartRun(context.Background(), "one")
	s1.End()
	_, s2 := tr.StartRun(context.Background(), "two")
	s2.End()

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(c.drained()); got != 2 {
		t.Fatalf("exported %d spans after shutdown, want 2", got)
	}
}

func TestEndWithError(t *testing.T) {
	c := &collector{}
	tr, _ := newTestTracer(t, c, tracer.Config{BatchSize: 1, FlushInterval: time.Hour})

	_, s := tr.StartRun(context.Background(), "failing")
	s.EndWithError(errors.New("boom"))

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	spans := c.drained()
	if len(spans) != 1 {
		t.Fatalf("exported %d spans, want 1", len(spans))
	}
	if spans[0].Status != span.StatusError || spans[0].Error != "boom" {
		t.Fatalf("span = %+v, want error status with message", spans[0])
	}
}

func TestEndIsIdempotent(t *testing.T) {
	c := &collector{}
	tr, _ := newTestTracer(t, c, tracer.Config{BatchSize: 1, FlushInterval: time.Hour})

	_, s := tr.StartRun(context.Background(), "once")
	s.End()
	s.End()

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(c.drained()); got != 1 {
		t.Fatalf("exported %d spans, want 1", got)
	}
}

func TestExportFailureReportsError(t *testing.T) {
	var (
		mu   sync.Mutex
		errs []error
	)
	tr := tracer.New(tracer.Config{
		Endpoint:      "http://127.0.0.1:1/v1/spans",
		BatchSize:     1,
		FlushInterval: time.Hour,
		OnError: func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		},
	})
	_, s := tr.StartRun(context.Background(), "offline")
	s.End()

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(errs) == 0 {
		t.Fatal("OnError not called for failed export")
	}
}

func TestConcurrentSpans(t *testing.T) {
	c := &collector{}
	tr, _ := newTestTracer(t, c, tracer.Config{BatchSize: 16, FlushInterval: 5 * time.Millisecond})

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, s := tr.StartRun(context.Background(), "run")
			s.SetAttribute("k", "v")
			s.End()
		}()
	}
	wg.Wait()

	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if got := len(c.drained()); got != n {
		t.Fatalf("exported %d spans, want %d", got, n)
	}
}
