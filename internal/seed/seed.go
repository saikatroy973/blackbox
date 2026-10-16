// Package seed populates a fresh database with demo traces, a replay, and
// eval runs so the dashboard has something interesting to show.
package seed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/internal/eval"
	"github.com/saikat/blackbox/internal/replay"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

// Questions is the default demo workload: research questions the mock
// LLM and tools know how to answer, plus one graceful no-results case.
var Questions = []string{
	"What is the capital of France and its population?",
	"Tell me about LLM agents.",
	"What is 42 * 17?",
	"Why is Go a good language for backend services?",
	"What is the population of Atlantis?",
	"How many people live in Paris?",
}

// Options configures a seed run.
type Options struct {
	// Runs limits how many Questions are recorded. Defaults to all.
	Runs int
	// EvalsDir is scanned for suite YAML files. Empty disables eval runs.
	EvalsDir string
}

// Report summarizes what was written.
type Report struct {
	Traces      []string
	ErrorTrace  string
	ReplayTrace string
	EvalSuites  int
	EvalCases   int
}

// Run records the demo workload into st.
func Run(ctx context.Context, st store.Store, opts Options) (Report, error) {
	questions := Questions
	if opts.Runs > 0 && opts.Runs < len(questions) {
		questions = questions[:opts.Runs]
	}

	var report Report
	for _, q := range questions {
		id, err := runAgent(ctx, st, q)
		if err != nil {
			return report, fmt.Errorf("seed %q: %w", q, err)
		}
		report.Traces = append(report.Traces, id)
	}

	errTrace, err := insertErrorTrace(ctx, st)
	if err != nil {
		return report, fmt.Errorf("seed error trace: %w", err)
	}
	report.ErrorTrace = errTrace

	if len(report.Traces) > 0 {
		res, err := replay.New(st).Run(ctx, report.Traces[0])
		if err != nil {
			return report, fmt.Errorf("seed replay: %w", err)
		}
		if res.Misses > 0 {
			return report, fmt.Errorf("seed replay diverged: %+v", res.Divergences)
		}
		report.ReplayTrace = res.ReplayTraceID
	}

	if opts.EvalsDir != "" {
		suites, cases, err := runEvals(ctx, st, opts.EvalsDir)
		if err != nil {
			return report, err
		}
		report.EvalSuites = suites
		report.EvalCases = cases
	}
	return report, nil
}

func runAgent(ctx context.Context, st store.Store, question string) (string, error) {
	tr := tracer.New(tracer.Config{
		Exporter:      tracer.ExporterFunc(st.InsertSpans),
		ServiceName:   "demoagent",
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	defer func() { _ = tr.Shutdown(ctx) }()
	agent := demoagent.New(demoagent.Config{
		Provider: llm.NewMock(),
		Tracer:   tr,
		Model:    "mock-1",
		Tags:     map[string]any{"blackbox.seeded": true},
	})
	res, err := agent.Run(ctx, question)
	if err != nil {
		return "", err
	}
	return res.TraceID, nil
}

// insertErrorTrace records a handcrafted failing run so the dashboard
// shows error styling out of the box.
func insertErrorTrace(ctx context.Context, st store.Store) (string, error) {
	traceID := newID()
	now := time.Now().UTC()
	mk := func(spanID, parent, name string, kind span.Kind, start, end time.Time, status span.Status) span.Span {
		return span.Span{
			TraceID: traceID, SpanID: spanID, ParentSpanID: parent, Name: name, Kind: kind,
			StartTime: start, EndTime: end, Status: status,
		}
	}

	root := mk("root", "", "research-agent", span.KindAgentRun, now, now.Add(1400*time.Millisecond), span.StatusError)
	root.SetAttribute(span.AttrAgentName, "research-agent")
	root.SetAttribute("blackbox.seeded", true)
	out, _ := json.Marshal(map[string]string{"answer": "", "error": "tool failed: upstream 429"})
	root.Output = out

	step := mk("step-1", "root", "step-1", span.KindAgentStep, now.Add(10*time.Millisecond), now.Add(1300*time.Millisecond), span.StatusOK)

	decide := mk("llm-1", "step-1", "decide", span.KindLLMCall, now.Add(20*time.Millisecond), now.Add(400*time.Millisecond), span.StatusOK)
	decide.SetAttribute(span.AttrModel, "mock-1")
	decide.SetAttribute(span.AttrCostUSD, 0.00021)
	decide.Input, _ = json.Marshal(map[string]string{"question": "What is the recent funding of Anthropic?"})
	decide.Output, _ = json.Marshal(map[string]any{"tool": "web_search", "args": map[string]string{"query": "Anthropic funding"}})

	call := mk("tool-1", "step-1", "web_search", span.KindToolCall, now.Add(500*time.Millisecond), now.Add(1200*time.Millisecond), span.StatusError)
	call.Error = "upstream 429: rate limited"
	call.SetAttribute(span.AttrToolName, "web_search")
	call.Input, _ = json.Marshal(map[string]string{"query": "Anthropic funding"})

	fail := mk("llm-2", "step-1", "finish", span.KindLLMCall, now.Add(1250*time.Millisecond), now.Add(1350*time.Millisecond), span.StatusError)
	fail.SetAttribute(span.AttrModel, "mock-1")
	fail.Error = "run aborted: tool failed"

	if err := st.InsertSpans(ctx, []span.Span{root, step, decide, call, fail}); err != nil {
		return "", err
	}
	return traceID, nil
}

func runEvals(ctx context.Context, st store.Store, dir string) (int, int, error) {
	var paths []string
	for _, pattern := range []string{"*.yaml", "*.yml"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil {
			return 0, 0, err
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return 0, 0, nil
	}

	runner := eval.New(st, llm.NewMock(), "mock-1")
	suites, cases := 0, 0
	for _, path := range paths {
		suite, err := eval.LoadSuite(path)
		if err != nil {
			return suites, cases, fmt.Errorf("%s: %w", path, err)
		}
		res, err := runner.Run(ctx, suite)
		if err != nil {
			return suites, cases, fmt.Errorf("%s: %w", path, err)
		}
		suites++
		cases += res.Total
	}
	return suites, cases, nil
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
