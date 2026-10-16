package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/saikat/blackbox/internal/eval"
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
	Title       string
	TraceID     string
	ShortID     string
	RootName    string
	Status      string
	Duration    time.Duration
	StartTime   time.Time
	SpanCount   int
	TotalCost   float64
	Nodes       []spanNode
	DiffOptions []diffOption
}

type diffOption struct {
	TraceID   string
	ShortID   string
	RootName  string
	StartTime time.Time
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

type evalsView struct {
	Title  string
	Suites []evalSuiteRow
}

type evalSuiteRow struct {
	Suite         string
	Runs          int
	LatestID      string
	LatestStarted time.Time
	LatestPassed  int
	LatestTotal   int
	LatestCost    float64
	SparkPoints   string
	SparkClass    string
}

func newEvalSuiteRows(runs []store.EvalRun) []evalSuiteRow {
	bySuite := make(map[string][]store.EvalRun)
	var order []string
	for _, r := range runs {
		if _, ok := bySuite[r.Suite]; !ok {
			order = append(order, r.Suite)
		}
		bySuite[r.Suite] = append(bySuite[r.Suite], r)
	}
	rows := make([]evalSuiteRow, 0, len(order))
	for _, suite := range order {
		list := bySuite[suite] // newest first
		latest := list[0]
		chrono := make([]store.EvalRun, 0, len(list))
		for i := len(list) - 1; i >= 0; i-- {
			chrono = append(chrono, list[i])
		}
		if len(chrono) > 20 {
			chrono = chrono[len(chrono)-20:]
		}
		class := "spark-error"
		if latest.Failed == 0 {
			class = "spark-ok"
		}
		rows = append(rows, evalSuiteRow{
			Suite:         suite,
			Runs:          len(list),
			LatestID:      latest.ID,
			LatestStarted: latest.StartedAt,
			LatestPassed:  latest.Passed,
			LatestTotal:   latest.Total,
			LatestCost:    latest.TotalCostUSD,
			SparkPoints:   sparkPoints(chrono),
			SparkClass:    class,
		})
	}
	return rows
}

// sparkPoints renders pass rates as an SVG polyline over a 100x30 box.
func sparkPoints(runs []store.EvalRun) string {
	y := func(r store.EvalRun) float64 {
		if r.Total == 0 {
			return 1
		}
		return 29 - float64(r.Passed)/float64(r.Total)*28
	}
	if len(runs) == 1 {
		return fmt.Sprintf("0,%.1f 100,%.1f", y(runs[0]), y(runs[0]))
	}
	var b strings.Builder
	for i, r := range runs {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", float64(i)*100/float64(len(runs)-1), y(r))
	}
	return b.String()
}

type evalRunView struct {
	Title      string
	ID         string
	ShortID    string
	Suite      string
	StartedAt  time.Time
	DurationMS int64
	Total      int
	Passed     int
	Failed     int
	TotalCost  float64
	Cases      []evalCaseRow
}

type evalCaseRow struct {
	Name       string
	Passed     bool
	Answer     string
	TraceID    string
	ShortTrace string
	Steps      int
	Tools      string
	CostUSD    float64
	DurationMS int64
	Failures   []string
}

func newEvalRunView(run store.EvalRun, cases []eval.CaseResult) evalRunView {
	rows := make([]evalCaseRow, 0, len(cases))
	for _, c := range cases {
		rows = append(rows, evalCaseRow{
			Name:       c.Name,
			Passed:     c.Passed,
			Answer:     c.Answer,
			TraceID:    c.TraceID,
			ShortTrace: shortID(c.TraceID),
			Steps:      c.Steps,
			Tools:      strings.Join(c.Tools, ", "),
			CostUSD:    c.CostUSD,
			DurationMS: c.DurationMS,
			Failures:   c.Failures,
		})
	}
	return evalRunView{
		Title:      "Eval " + run.Suite,
		ID:         run.ID,
		ShortID:    shortID(run.ID),
		Suite:      run.Suite,
		StartedAt:  run.StartedAt,
		DurationMS: run.DurationMS,
		Total:      run.Total,
		Passed:     run.Passed,
		Failed:     run.Failed,
		TotalCost:  run.TotalCostUSD,
		Cases:      rows,
	}
}
