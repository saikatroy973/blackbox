package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

// Row kinds for trace diffs.
const (
	diffSame    = "same"
	diffChanged = "changed"
	diffAdded   = "added"
	diffRemoved = "removed"
)

type diffTraceHead struct {
	TraceID   string
	ShortID   string
	RootName  string
	Status    string
	Duration  time.Duration
	Cost      float64
	Spans     int
	StartTime time.Time
}

type diffSpanCell struct {
	SpanID    string
	Name      string
	Kind      string
	KindClass string
	Status    string
	Duration  time.Duration
	IndentPx  int
}

type diffRow struct {
	Kind       string
	Left       *diffSpanCell
	Right      *diffSpanCell
	Changes    []string
	InputDiff  []diffLine
	OutputDiff []diffLine
}

type diffLine struct {
	Class  string
	Prefix string
	Text   string
}

type diffSummary struct {
	Same    int
	Changed int
	Added   int
	Removed int
}

type diffView struct {
	Title   string
	Left    diffTraceHead
	Right   diffTraceHead
	Summary diffSummary
	Rows    []diffRow
}

func newDiffTraceHead(id string, spans []span.Span) diffTraceHead {
	_, start, end := buildNodes(spans)
	return diffTraceHead{
		TraceID:   id,
		ShortID:   shortID(id),
		RootName:  rootName(spans),
		Status:    traceStatus(spans),
		Duration:  end.Sub(start),
		Cost:      totalCost(spans),
		Spans:     len(spans),
		StartTime: start,
	}
}

// alignSpans pairs up the spans of two traces by position in a shared
// (kind, name) subsequence, marking leftovers as added or removed.
func alignSpans(left, right []span.Span) ([]diffRow, diffSummary) {
	sig := func(s span.Span) string { return string(s.Kind) + "\x00" + s.Name }

	lNodes, _, _ := buildNodes(left)
	rNodes, _, _ := buildNodes(right)
	lSpans := make([]span.Span, len(lNodes))
	for i, n := range lNodes {
		lSpans[i] = n.Span
	}
	rSpans := make([]span.Span, len(rNodes))
	for i, n := range rNodes {
		rSpans[i] = n.Span
	}
	lDepth := make([]int, len(lNodes))
	for i, n := range lNodes {
		lDepth[i] = n.Depth
	}
	rDepth := make([]int, len(rNodes))
	for i, n := range rNodes {
		rDepth[i] = n.Depth
	}

	n, m := len(lSpans), len(rSpans)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if sig(lSpans[i]) == sig(rSpans[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var rows []diffRow
	var summary diffSummary
	i, j := 0, 0
	for i < n && j < m {
		if sig(lSpans[i]) == sig(rSpans[j]) {
			row := diffRow{
				Kind:  diffSame,
				Left:  newSpanCell(lSpans[i], lDepth[i]),
				Right: newSpanCell(rSpans[j], rDepth[j]),
			}
			if row.InputDiff = payloadDiff(lSpans[i].Input, rSpans[j].Input); row.InputDiff != nil {
				row.Changes = append(row.Changes, "input changed")
			}
			if row.OutputDiff = payloadDiff(lSpans[i].Output, rSpans[j].Output); row.OutputDiff != nil {
				row.Changes = append(row.Changes, "output changed")
			}
			row.Changes = append(row.Changes, spanChanges(lSpans[i], rSpans[j])...)
			if len(row.Changes) > 0 {
				row.Kind = diffChanged
				summary.Changed++
			} else {
				summary.Same++
			}
			rows = append(rows, row)
			i++
			j++
			continue
		}
		if lcs[i+1][j] >= lcs[i][j+1] {
			rows = append(rows, diffRow{Kind: diffRemoved, Left: newSpanCell(lSpans[i], lDepth[i])})
			summary.Removed++
			i++
		} else {
			rows = append(rows, diffRow{Kind: diffAdded, Right: newSpanCell(rSpans[j], rDepth[j])})
			summary.Added++
			j++
		}
	}
	for ; i < n; i++ {
		rows = append(rows, diffRow{Kind: diffRemoved, Left: newSpanCell(lSpans[i], lDepth[i])})
		summary.Removed++
	}
	for ; j < m; j++ {
		rows = append(rows, diffRow{Kind: diffAdded, Right: newSpanCell(rSpans[j], rDepth[j])})
		summary.Added++
	}
	return rows, summary
}

func newSpanCell(s span.Span, depth int) *diffSpanCell {
	return &diffSpanCell{
		SpanID:    s.SpanID,
		Name:      s.Name,
		Kind:      string(s.Kind),
		KindClass: kindClass(s.Kind),
		Status:    string(s.Status),
		Duration:  s.Duration(),
		IndentPx:  depth * 18,
	}
}

func spanChanges(a, b span.Span) []string {
	var changes []string
	if a.Status != b.Status {
		changes = append(changes, fmt.Sprintf("status: %s → %s", a.Status, b.Status))
	}
	if a.Error != b.Error && b.Error != "" {
		changes = append(changes, "error: "+b.Error)
	}
	if d := b.Duration() - a.Duration(); math.Abs(float64(d)) >= float64(time.Millisecond) {
		changes = append(changes, "duration: "+signedDuration(d))
	}
	if ca, cb := attrCost(a), attrCost(b); cb != ca {
		changes = append(changes, fmt.Sprintf("cost: %+.6f", cb-ca))
	}
	return changes
}

func attrCost(s span.Span) float64 {
	v, _ := s.Attributes[span.AttrCostUSD].(float64)
	return v
}

func signedDuration(d time.Duration) string {
	if d < 0 {
		return "-" + formatDuration(-d)
	}
	return "+" + formatDuration(d)
}

// payloadDiff returns a line diff for two payloads, or nil if they are
// semantically equal.
func payloadDiff(a, b json.RawMessage) []diffLine {
	if equalJSON(a, b) {
		return nil
	}
	return diffLines(jsonPretty(a), jsonPretty(b))
}

func equalJSON(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return string(a) == string(b)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return string(a) == string(b)
	}
	return reflect.DeepEqual(av, bv)
}

// diffLines computes a line-by-line diff of two strings using LCS.
func diffLines(a, b string) []diffLine {
	if a == b {
		return nil
	}
	al := splitLines(a)
	bl := splitLines(b)

	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var out []diffLine
	i, j := 0, 0
	for i < n && j < m {
		if al[i] == bl[j] {
			out = append(out, diffLine{Class: "dl-same", Prefix: " ", Text: al[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			out = append(out, diffLine{Class: "dl-del", Prefix: "-", Text: al[i]})
			i++
		} else {
			out = append(out, diffLine{Class: "dl-add", Prefix: "+", Text: bl[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffLine{Class: "dl-del", Prefix: "-", Text: al[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffLine{Class: "dl-add", Prefix: "+", Text: bl[j]})
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
