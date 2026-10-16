// Package span defines the Blackbox span model and agent semantic
// conventions. It is the wire format spoken between the tracer SDK, the
// ingest endpoint, and the store.
package span

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Kind classifies what a span represents. Unknown kinds are accepted for
// forward compatibility.
type Kind string

const (
	KindAgentRun  Kind = "agent.run"
	KindAgentStep Kind = "agent.step"
	KindLLMCall   Kind = "llm.call"
	KindToolCall  Kind = "tool.call"
	KindRetrieval Kind = "retrieval"
	KindMemory    Kind = "memory"
	KindHandoff   Kind = "handoff"
)

// Status is the outcome of a span.
type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// Attribute keys. Where an OpenTelemetry GenAI semantic convention exists
// it is used verbatim; Blackbox-specific keys are prefixed.
const (
	AttrServiceName  = "service.name"
	AttrAgentName    = "gen_ai.agent.name"
	AttrModel        = "gen_ai.request.model"
	AttrInputTokens  = "gen_ai.usage.input_tokens"
	AttrOutputTokens = "gen_ai.usage.output_tokens"
	AttrToolName     = "gen_ai.tool.name"
	AttrCostUSD      = "blackbox.cost_usd"
	AttrTruncated    = "blackbox.truncated"
)

// ErrInvalid marks span validation failures.
var ErrInvalid = errors.New("invalid span")

// Span is a single unit of work within an agent trace.
type Span struct {
	TraceID      string          `json:"trace_id"`
	SpanID       string          `json:"span_id"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	Name         string          `json:"name"`
	Kind         Kind            `json:"kind"`
	StartTime    time.Time       `json:"start_time"`
	EndTime      time.Time       `json:"end_time"`
	Status       Status          `json:"status"`
	Error        string          `json:"error,omitempty"`
	Attributes   map[string]any  `json:"attributes,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Output       json.RawMessage `json:"output,omitempty"`
}

// Duration returns the span's wall-clock duration.
func (s Span) Duration() time.Duration {
	if s.EndTime.IsZero() || s.EndTime.Before(s.StartTime) {
		return 0
	}
	return s.EndTime.Sub(s.StartTime)
}

// Validate checks that the span has the fields required to be stored.
func (s Span) Validate() error {
	switch {
	case s.TraceID == "":
		return fmt.Errorf("%w: trace_id is required", ErrInvalid)
	case s.SpanID == "":
		return fmt.Errorf("%w: span_id is required", ErrInvalid)
	case s.Name == "":
		return fmt.Errorf("%w: name is required", ErrInvalid)
	case s.Kind == "":
		return fmt.Errorf("%w: kind is required", ErrInvalid)
	case s.StartTime.IsZero():
		return fmt.Errorf("%w: start_time is required", ErrInvalid)
	case s.EndTime.Before(s.StartTime):
		return fmt.Errorf("%w: end_time %s is before start_time %s",
			ErrInvalid, s.EndTime.Format(time.RFC3339Nano), s.StartTime.Format(time.RFC3339Nano))
	}
	return nil
}

// SetAttribute sets an attribute on the span.
func (s *Span) SetAttribute(key string, value any) {
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	s.Attributes[key] = value
}

// CapPayloads replaces input and output payloads larger than max bytes
// with a truncated preview and flags the span. It reports whether anything
// was truncated.
func (s *Span) CapPayloads(max int) bool {
	if max <= 0 {
		return false
	}
	truncated := false
	if len(s.Input) > max {
		s.Input = truncatedPayload(s.Input, max)
		truncated = true
	}
	if len(s.Output) > max {
		s.Output = truncatedPayload(s.Output, max)
		truncated = true
	}
	if truncated {
		s.SetAttribute(AttrTruncated, true)
	}
	return truncated
}

func truncatedPayload(raw json.RawMessage, max int) json.RawMessage {
	preview := raw
	if len(preview) > max {
		preview = preview[:max]
	}
	out, err := json.Marshal(map[string]any{
		"truncated":      true,
		"original_bytes": len(raw),
		"preview":        string(preview),
	})
	if err != nil {
		return json.RawMessage(`{"truncated":true}`)
	}
	return out
}
