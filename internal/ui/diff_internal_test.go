package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func TestDiffLines(t *testing.T) {
	lines := diffLines("a\nb\nc", "a\nc\nd")
	var got []string
	for _, l := range lines {
		got = append(got, l.Prefix+l.Text)
	}
	want := []string{" a", "-b", " c", "+d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("diff = %v, want %v", got, want)
	}
	if diffLines("same", "same") != nil {
		t.Fatal("identical inputs should produce no diff")
	}
}

func TestAlignSpansChangedAddedRemoved(t *testing.T) {
	now := time.Now()
	left := []span.Span{
		{TraceID: "l", SpanID: "1", Name: "agent", Kind: span.KindAgentRun, StartTime: now, EndTime: now.Add(time.Second)},
		{TraceID: "l", SpanID: "2", ParentSpanID: "1", Name: "plan", Kind: span.KindLLMCall, StartTime: now, EndTime: now.Add(100 * time.Millisecond), Output: []byte(`{"a":1}`)},
		{TraceID: "l", SpanID: "3", ParentSpanID: "1", Name: "kb", Kind: span.KindRetrieval, StartTime: now, EndTime: now.Add(50 * time.Millisecond)},
		{TraceID: "l", SpanID: "4", ParentSpanID: "1", Name: "tool", Kind: span.KindToolCall, StartTime: now, EndTime: now.Add(10 * time.Millisecond)},
	}
	right := []span.Span{
		{TraceID: "r", SpanID: "1", Name: "agent", Kind: span.KindAgentRun, StartTime: now, EndTime: now.Add(2 * time.Second)},
		{TraceID: "r", SpanID: "2", ParentSpanID: "1", Name: "plan", Kind: span.KindLLMCall, StartTime: now, EndTime: now.Add(100 * time.Millisecond), Output: []byte(`{"a":2}`)},
		{TraceID: "r", SpanID: "3", ParentSpanID: "1", Name: "kb", Kind: span.KindRetrieval, StartTime: now, EndTime: now.Add(50 * time.Millisecond)},
		{TraceID: "r", SpanID: "5", ParentSpanID: "1", Name: "extra", Kind: span.KindToolCall, StartTime: now, EndTime: now.Add(5 * time.Millisecond)},
	}

	rows, summary := alignSpans(left, right)
	if summary.Same != 1 || summary.Changed != 2 || summary.Added != 1 || summary.Removed != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	kinds := make([]string, len(rows))
	for i, r := range rows {
		kinds[i] = r.Kind
	}
	if strings.Join(kinds, ",") != "changed,changed,same,removed,added" {
		t.Fatalf("row kinds = %v", kinds)
	}
	if rows[1].OutputDiff == nil || len(rows[1].Changes) == 0 {
		t.Fatalf("changed llm row missing payload diff: %+v", rows[1])
	}
}

func TestSparkPoints(t *testing.T) {
	mk := func(passed, total int) store.EvalRun {
		return store.EvalRun{Passed: passed, Total: total}
	}
	if got := sparkPoints([]store.EvalRun{mk(2, 2)}); got != "0,1.0 100,1.0" {
		t.Fatalf("single run = %q", got)
	}
	got := sparkPoints([]store.EvalRun{mk(0, 2), mk(2, 2)})
	if got != "0.0,29.0 100.0,1.0" {
		t.Fatalf("two runs = %q", got)
	}
}

func TestSpanChanges(t *testing.T) {
	now := time.Now()
	a := span.Span{Status: span.StatusOK, StartTime: now, EndTime: now.Add(time.Second), Attributes: map[string]any{span.AttrCostUSD: 0.001}}
	b := span.Span{Status: span.StatusError, Error: "boom", StartTime: now, EndTime: now.Add(3 * time.Second), Attributes: map[string]any{span.AttrCostUSD: 0.0015}}
	changes := strings.Join(spanChanges(a, b), "\n")
	for _, want := range []string{"status: ok → error", "error: boom", "duration: +2.00s", "+0.000500"} {
		if !strings.Contains(changes, want) {
			t.Fatalf("changes missing %q: %s", want, changes)
		}
	}
	if got := spanChanges(a, a); len(got) != 0 {
		t.Fatalf("identical spans changed: %v", got)
	}
}
