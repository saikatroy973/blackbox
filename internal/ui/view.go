package ui

import (
	"fmt"
	"sort"
	"time"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

const shortIDLen = 12

func shortID(id string) string {
	if len(id) <= shortIDLen {
		return id
	}
	return id[:shortIDLen]
}

type listView struct {
	Title  string
	Status string
	Limit  int
	Traces []traceRow
}

type traceRow struct {
	TraceID    string
	ShortID    string
	RootName   string
	Status     string
	Duration   time.Duration
	StartTime  time.Time
	SpanCount  int
	ErrorCount int
}

func newTraceRows(sums []store.TraceSummary) []traceRow {
	rows := make([]traceRow, 0, len(sums))
	for _, s := range sums {
		status := "ok"
		if s.ErrorCount > 0 {
			status = "error"
		}
		rows = append(rows, traceRow{
			TraceID:    s.TraceID,
			ShortID:    shortID(s.TraceID),
			RootName:   s.RootName,
			Status:     status,
			Duration:   s.EndTime.Sub(s.StartTime),
			StartTime:  s.StartTime,
			SpanCount:  s.SpanCount,
			ErrorCount: s.ErrorCount,
		})
	}
	return rows
}

type tracePageView struct {
	Title     string
	TraceID   string
	ShortID   string
	RootName  string
	Status    string
	Duration  time.Duration
	StartTime time.Time
	SpanCount int
	TotalCost float64
	Nodes     []spanNode
}

type spanNode struct {
	Span      span.Span
	Depth     int
	IndentPx  int
	Duration  time.Duration
	LeftPct   float64
	WidthPct  float64
	KindClass string
}

type spanPanelView struct {
	Span         span.Span
	Duration     time.Duration
	KindClass    string
	InputPretty  string
	OutputPretty string
}

// buildNodes flattens the span tree in depth-first order and computes
// waterfall geometry relative to the trace window.
func buildNodes(spans []span.Span) (nodes []spanNode, start, end time.Time) {
	if len(spans) == 0 {
		return nil, time.Time{}, time.Time{}
	}

	start, end = spans[0].StartTime, spans[0].EndTime
	for _, s := range spans {
		if s.StartTime.Before(start) {
			start = s.StartTime
		}
		if s.EndTime.After(end) {
			end = s.EndTime
		}
	}
	total := end.Sub(start)
	if total <= 0 {
		total = time.Millisecond
	}

	known := make(map[string]bool, len(spans))
	for _, s := range spans {
		known[s.SpanID] = true
	}

	children := make(map[string][]span.Span)
	var roots []span.Span
	for _, s := range spans {
		if s.ParentSpanID == "" || !known[s.ParentSpanID] {
			roots = append(roots, s)
		} else {
			children[s.ParentSpanID] = append(children[s.ParentSpanID], s)
		}
	}

	sortSpans := func(list []span.Span) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].StartTime.Equal(list[j].StartTime) {
				return list[i].SpanID < list[j].SpanID
			}
			return list[i].StartTime.Before(list[j].StartTime)
		})
	}
	sortSpans(roots)
	for _, list := range children {
		sortSpans(list)
	}

	nodes = make([]spanNode, 0, len(spans))
	var walk func(s span.Span, depth int)
	walk = func(s span.Span, depth int) {
		left := float64(s.StartTime.Sub(start)) / float64(total)
		width := float64(s.Duration()) / float64(total)
		if width < 0.005 {
			width = 0.005
		}
		if left < 0 {
			left = 0
		}
		if left > 1 {
			left = 1
		}
		if left+width > 1 {
			left = 1 - width
			if left < 0 {
				left, width = 0, 1
			}
		}
		nodes = append(nodes, spanNode{
			Span:      s,
			Depth:     depth,
			IndentPx:  depth * 18,
			Duration:  s.Duration(),
			LeftPct:   left * 100,
			WidthPct:  width * 100,
			KindClass: kindClass(s.Kind),
		})
		for _, c := range children[s.SpanID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return nodes, start, end
}

func rootName(spans []span.Span) string {
	for _, s := range spans {
		if s.ParentSpanID == "" {
			return s.Name
		}
	}
	if len(spans) > 0 {
		return spans[0].Name
	}
	return ""
}

func totalCost(spans []span.Span) float64 {
	var sum float64
	for _, s := range spans {
		if v, ok := s.Attributes[span.AttrCostUSD].(float64); ok {
			sum += v
		}
	}
	return sum
}

func traceStatus(spans []span.Span) string {
	for _, s := range spans {
		if s.Status == span.StatusError {
			return "error"
		}
	}
	return "ok"
}

func kindClass(k span.Kind) string {
	switch k {
	case span.KindAgentRun:
		return "kind-agent"
	case span.KindAgentStep:
		return "kind-step"
	case span.KindLLMCall:
		return "kind-llm"
	case span.KindToolCall:
		return "kind-tool"
	case span.KindRetrieval:
		return "kind-retrieval"
	case span.KindMemory:
		return "kind-memory"
	case span.KindHandoff:
		return "kind-handoff"
	default:
		return "kind-step"
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.2fs", d.Seconds())
	default:
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
}

func formatCost(c float64) string {
	return fmt.Sprintf("$%.6f", c)
}
