package eval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

// CaseResult is the outcome of one eval case.
type CaseResult struct {
	Name       string   `json:"name"`
	Question   string   `json:"question"`
	Passed     bool     `json:"passed"`
	Failures   []string `json:"failures,omitempty"`
	Error      string   `json:"error,omitempty"`
	Answer     string   `json:"answer"`
	TraceID    string   `json:"trace_id"`
	Steps      int      `json:"steps"`
	Tools      []string `json:"tools,omitempty"`
	CostUSD    float64  `json:"cost_usd"`
	DurationMS int64    `json:"duration_ms"`
}

func (r CaseResult) usedTool(name string) bool {
	for _, t := range r.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// RunResult is the outcome of a whole suite.
type RunResult struct {
	ID           string       `json:"id"`
	Suite        string       `json:"suite"`
	StartedAt    time.Time    `json:"started_at"`
	DurationMS   int64        `json:"duration_ms"`
	Total        int          `json:"total"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	TotalCostUSD float64      `json:"total_cost_usd"`
	Cases        []CaseResult `json:"cases"`
}

// Runner executes suites.
type Runner struct {
	store    store.Store
	provider llm.Provider
	model    string
}

// New returns a Runner that records one trace per case into st and drives
// agents with provider.
func New(st store.Store, provider llm.Provider, model string) *Runner {
	return &Runner{store: st, provider: provider, model: model}
}

// Run executes every case, records one trace per case, and persists the
// suite result.
func (r *Runner) Run(ctx context.Context, suite Suite) (RunResult, error) {
	res := RunResult{
		ID:        newID(),
		Suite:     suite.Name,
		StartedAt: time.Now().UTC(),
		Total:     len(suite.Cases),
	}
	for _, c := range suite.Cases {
		cr := r.runCase(ctx, suite, c)
		if cr.Passed {
			res.Passed++
		} else {
			res.Failed++
		}
		res.TotalCostUSD += cr.CostUSD
		res.Cases = append(res.Cases, cr)
	}
	res.DurationMS = time.Since(res.StartedAt).Milliseconds()

	results, err := json.Marshal(res.Cases)
	if err != nil {
		return res, err
	}
	err = r.store.InsertEvalRun(ctx, store.EvalRun{
		ID:           res.ID,
		Suite:        res.Suite,
		StartedAt:    res.StartedAt,
		DurationMS:   res.DurationMS,
		Total:        res.Total,
		Passed:       res.Passed,
		Failed:       res.Failed,
		TotalCostUSD: res.TotalCostUSD,
		Results:      results,
	})
	return res, err
}

func (r *Runner) runCase(ctx context.Context, suite Suite, c Case) CaseResult {
	cr := CaseResult{Name: c.Name, Question: c.Question}

	tr := tracer.New(tracer.Config{
		Exporter:      tracer.ExporterFunc(r.store.InsertSpans),
		ServiceName:   "eval",
		BatchSize:     32,
		FlushInterval: 100 * time.Millisecond,
	})
	agent := demoagent.New(demoagent.Config{
		Provider: r.provider,
		Tracer:   tr,
		Model:    r.model,
		Tags: map[string]any{
			"blackbox.eval_suite": suite.Name,
			"blackbox.eval_case":  c.Name,
		},
	})
	run, err := agent.Run(ctx, c.Question)
	cr.TraceID = run.TraceID
	cr.Answer = run.Answer
	if err != nil {
		cr.Error = err.Error()
	}
	if shutErr := tr.Shutdown(ctx); shutErr != nil && cr.Error == "" {
		cr.Error = shutErr.Error()
	}

	if spans, err := r.store.GetTrace(ctx, cr.TraceID); err == nil {
		m := metricsFromSpans(spans)
		cr.Steps = m.steps
		cr.Tools = m.tools
		cr.CostUSD = m.costUSD
		cr.DurationMS = m.durationMS
		if cr.Answer == "" {
			cr.Answer = m.answer
		}
	}

	if cr.Error != "" {
		cr.Failures = append(cr.Failures, "agent error: "+cr.Error)
	}
	cr.Failures = append(cr.Failures, Check(cr, c.Expect)...)
	cr.Passed = len(cr.Failures) == 0
	return cr
}

type traceMetrics struct {
	steps      int
	tools      []string
	costUSD    float64
	durationMS int64
	answer     string
}

func metricsFromSpans(spans []span.Span) traceMetrics {
	var m traceMetrics
	var root *span.Span
	for i := range spans {
		s := &spans[i]
		switch s.Kind {
		case span.KindAgentStep:
			m.steps++
		case span.KindToolCall:
			m.tools = append(m.tools, s.Name)
		}
		if cost, ok := s.Attributes[span.AttrCostUSD].(float64); ok {
			m.costUSD += cost
		}
		if s.ParentSpanID == "" {
			root = s
		}
	}
	if root != nil {
		m.durationMS = root.Duration().Milliseconds()
		var payload struct {
			Answer string `json:"answer"`
		}
		if json.Unmarshal(root.Output, &payload) == nil {
			m.answer = payload.Answer
		}
	}
	return m
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
