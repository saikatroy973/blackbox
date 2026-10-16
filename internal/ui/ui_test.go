package ui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/internal/ui"
	"github.com/saikat/blackbox/pkg/span"
)

func seed(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	spans := []span.Span{
		{
			TraceID: "trace-1", SpanID: "s1", Name: "research-agent", Kind: span.KindAgentRun,
			StartTime: now, EndTime: now.Add(2 * time.Second), Status: span.StatusOK,
			Attributes: map[string]any{span.AttrAgentName: "research-agent"},
			Output:     json.RawMessage(`{"answer": "Paris"}`),
		},
		{
			TraceID: "trace-1", SpanID: "s2", ParentSpanID: "s1", Name: "step-1", Kind: span.KindAgentStep,
			StartTime: now.Add(10 * time.Millisecond), EndTime: now.Add(900 * time.Millisecond), Status: span.StatusOK,
		},
		{
			TraceID: "trace-1", SpanID: "s3", ParentSpanID: "s2", Name: "plan", Kind: span.KindLLMCall,
			StartTime: now.Add(20 * time.Millisecond), EndTime: now.Add(600 * time.Millisecond), Status: span.StatusOK,
			Attributes: map[string]any{span.AttrModel: "mock-1", span.AttrCostUSD: 0.0002},
			Input:      json.RawMessage(`{"query":"capital of france"}`),
			Output:     json.RawMessage(`{"tool":"web_search"}`),
		},
		{
			TraceID: "trace-1", SpanID: "s4", ParentSpanID: "s2", Name: "web_search", Kind: span.KindToolCall,
			StartTime: now.Add(700 * time.Millisecond), EndTime: now.Add(800 * time.Millisecond), Status: span.StatusError,
			Error: "rate limited",
		},
		{
			TraceID: "trace-2", SpanID: "r1", Name: "other-agent", Kind: span.KindAgentRun,
			StartTime: now.Add(time.Hour), EndTime: now.Add(time.Hour + time.Second), Status: span.StatusOK,
		},
		{
			TraceID: "trace-3", SpanID: "c1", Name: "research-agent", Kind: span.KindAgentRun,
			StartTime: now.Add(2 * time.Hour), EndTime: now.Add(2*time.Hour + 3*time.Second), Status: span.StatusOK,
			Output: json.RawMessage(`{"answer": "Paris"}`),
		},
		{
			TraceID: "trace-3", SpanID: "c2", ParentSpanID: "c1", Name: "step-1", Kind: span.KindAgentStep,
			StartTime: now.Add(2*time.Hour + 10*time.Millisecond), EndTime: now.Add(2*time.Hour + 900*time.Millisecond), Status: span.StatusOK,
		},
		{
			TraceID: "trace-3", SpanID: "c3", ParentSpanID: "c2", Name: "plan", Kind: span.KindLLMCall,
			StartTime: now.Add(2*time.Hour + 20*time.Millisecond), EndTime: now.Add(2*time.Hour + 600*time.Millisecond), Status: span.StatusOK,
			Attributes: map[string]any{span.AttrModel: "mock-1", span.AttrCostUSD: 0.0003},
			Input:      json.RawMessage(`{"query":"capital of france"}`),
			Output:     json.RawMessage(`{"tool":"calculator"}`),
		},
		{
			TraceID: "trace-3", SpanID: "c4", ParentSpanID: "c2", Name: "web_search", Kind: span.KindToolCall,
			StartTime: now.Add(2*time.Hour + 700*time.Millisecond), EndTime: now.Add(2*time.Hour + 800*time.Millisecond), Status: span.StatusOK,
		},
		{
			TraceID: "trace-3", SpanID: "c5", ParentSpanID: "c2", Name: "summarize", Kind: span.KindToolCall,
			StartTime: now.Add(2*time.Hour + 850*time.Millisecond), EndTime: now.Add(2*time.Hour + 950*time.Millisecond), Status: span.StatusOK,
		},
	}
	if err := st.InsertSpans(context.Background(), spans); err != nil {
		t.Fatalf("insert: %v", err)
	}

	run := store.EvalRun{
		ID: "eval-1", Suite: "research-basics", StartedAt: now.Add(3 * time.Hour),
		DurationMS: 9, Total: 2, Passed: 1, Failed: 1, TotalCostUSD: 0.0004,
		Results: json.RawMessage(`[
			{"name":"capital","passed":true,"answer":"Paris","trace_id":"trace-1","steps":2,"cost_usd":0.0002,"duration_ms":4},
			{"name":"math","passed":false,"failures":["output does not contain \"56088\""],"answer":"The answer is 4.","trace_id":"trace-1","steps":2,"cost_usd":0.0002,"duration_ms":5}
		]`),
	}
	if err := st.InsertEvalRun(context.Background(), run); err != nil {
		t.Fatalf("insert eval run: %v", err)
	}

	h, err := ui.New(st)
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	return mux
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestListPage(t *testing.T) {
	rec := get(t, seed(t), "/ui/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"research-agent", "other-agent", "badge-ok", "badge-error",
		"/v1/events", "live-dot", "every 30s, refresh",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
}

func TestListPageFragment(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ui/", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	seed(t).ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "<!doctype") || strings.Contains(body, "<html") {
		t.Fatal("HTMX fragment rendered a full page")
	}
	if !strings.Contains(body, "research-agent") {
		t.Fatal("fragment missing trace rows")
	}
}

func TestListPageStatusFilter(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces?status=error")
	body := rec.Body.String()
	if !strings.Contains(body, "research-agent") {
		t.Fatal("error trace missing from filtered list")
	}
	if strings.Contains(body, "other-agent") {
		t.Fatal("ok trace should be filtered out")
	}
}

func TestTracePage(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces/trace-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"research-agent", "step-1", "plan", "web_search",
		"kind-llm", "kind-tool", "wf-bar", "badge-error", "4 spans",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("trace page missing %q", want)
		}
	}
}

func TestTracePageNotFound(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSpanPanel(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces/trace-1/spans/s3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"mock-1", "capital of france", "web_search", "0.0002"} {
		if !strings.Contains(body, want) {
			t.Fatalf("span panel missing %q", want)
		}
	}
}

func TestSpanPanelError(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces/trace-1/spans/s4")
	if !strings.Contains(rec.Body.String(), "rate limited") {
		t.Fatal("span panel missing error message")
	}

	rec = get(t, seed(t), "/ui/traces/trace-1/spans/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestTracePageDiffOptions(t *testing.T) {
	rec := get(t, seed(t), "/ui/traces/trace-1")
	body := rec.Body.String()
	for _, want := range []string{"compare with", "/ui/diff", "trace-2", "trace-3"} {
		if !strings.Contains(body, want) {
			t.Fatalf("trace page missing %q", want)
		}
	}
	if strings.Contains(body, `option value="trace-1"`) {
		t.Fatal("trace page offers to diff the trace with itself")
	}
}

func TestDiffPage(t *testing.T) {
	rec := get(t, seed(t), "/ui/diff?left=trace-1&right=trace-3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"1 same", "3 changed", "1 added", "0 removed",
		"status: error → ok",
		"output changed",
		"duration: &#43;1.00s",
		"summarize",
		"output diff",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diff page missing %q", want)
		}
	}
}

func TestDiffPageRemovedSpans(t *testing.T) {
	rec := get(t, seed(t), "/ui/diff?left=trace-1&right=trace-2")
	body := rec.Body.String()
	if !strings.Contains(body, "4 removed") || !strings.Contains(body, "1 added") {
		t.Fatalf("diff page summary missing: %s", body)
	}
}

func TestDiffPageErrors(t *testing.T) {
	rec := get(t, seed(t), "/ui/diff")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	rec = get(t, seed(t), "/ui/diff?left=trace-1&right=missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEvalsPage(t *testing.T) {
	rec := get(t, seed(t), "/ui/evals")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"research-basics", "1/2", "spark-error", "polyline"} {
		if !strings.Contains(body, want) {
			t.Fatalf("evals page missing %q", want)
		}
	}
}

func TestEvalRunPage(t *testing.T) {
	rec := get(t, seed(t), "/ui/evals/eval-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"research-basics", "1 failing", "capital", "math",
		"badge-ok", "badge-error",
		`output does not contain &#34;56088&#34;`,
		`/ui/traces/trace-1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("eval run page missing %q", want)
		}
	}
}

func TestEvalRunPageNotFound(t *testing.T) {
	rec := get(t, seed(t), "/ui/evals/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
