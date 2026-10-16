package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

// Report summarizes a replay of one trace.
type Report struct {
	TraceID       string
	ReplayTraceID string
	Question      string
	Spans         int
	Cassettes     int
	Hits          int
	Misses        int
	Divergences   []Divergence
	Answer        string
	RunErr        error
}

// Runner replays recorded traces against new spans written back into the
// same store.
type Runner struct {
	store store.Store
}

// New returns a Runner backed by st.
func New(st store.Store) *Runner {
	return &Runner{store: st}
}

// Run replays the trace with the given ID. The replay itself is recorded
// as a fresh trace (tagged blackbox.replay_of) so it can be inspected in
// the dashboard.
func (r *Runner) Run(ctx context.Context, traceID string) (Report, error) {
	spans, err := r.store.GetTrace(ctx, traceID)
	if err != nil {
		return Report{}, err
	}

	question := questionFromSpans(spans)
	if question == "" {
		return Report{}, fmt.Errorf("replay: trace %s has no recorded question; only demoagent runs can be replayed", traceID)
	}

	set := FromSpans(spans)
	log := NewLogger()

	tr := tracer.New(tracer.Config{
		Exporter:      tracer.ExporterFunc(r.store.InsertSpans),
		ServiceName:   "replay",
		BatchSize:     16,
		FlushInterval: 100 * time.Millisecond,
	})

	agent := demoagent.New(demoagent.Config{
		Provider: NewProvider(set, log),
		Tracer:   tr,
		Tools:    ToolRegistry(demoagent.DefaultRegistry(), set, log),
		Model:    modelFromSpans(spans),
		Tags:     map[string]any{span.AttrReplayOf: traceID},
	})

	report := Report{
		TraceID:   traceID,
		Question:  question,
		Spans:     len(spans),
		Cassettes: set.Len(),
	}

	res, runErr := agent.Run(ctx, question)
	if err := tr.Shutdown(ctx); err != nil {
		return report, err
	}

	report.ReplayTraceID = res.TraceID
	report.Answer = res.Answer
	report.Hits = log.Hits()
	report.Divergences = log.Divergences()
	report.Misses = len(report.Divergences)
	report.RunErr = runErr
	return report, nil
}

func questionFromSpans(spans []span.Span) string {
	for _, s := range spans {
		if s.ParentSpanID != "" {
			continue
		}
		var payload struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(s.Input, &payload); err != nil {
			continue
		}
		if payload.Question != "" {
			return payload.Question
		}
	}
	return ""
}

func modelFromSpans(spans []span.Span) string {
	for _, s := range spans {
		if s.Kind != span.KindLLMCall {
			continue
		}
		if model, ok := s.Attributes[span.AttrModel].(string); ok {
			return model
		}
	}
	return ""
}
