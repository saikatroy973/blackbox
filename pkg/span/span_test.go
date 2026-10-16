package span_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

func valid() span.Span {
	now := time.Now()
	return span.Span{
		TraceID:   "trace",
		SpanID:    "span",
		Name:      "call",
		Kind:      span.KindLLMCall,
		StartTime: now,
		EndTime:   now.Add(time.Millisecond),
		Status:    span.StatusOK,
	}
}

func TestValidateAcceptsValidSpan(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsInvalidSpans(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*span.Span)
	}{
		{"missing trace id", func(s *span.Span) { s.TraceID = "" }},
		{"missing span id", func(s *span.Span) { s.SpanID = "" }},
		{"missing name", func(s *span.Span) { s.Name = "" }},
		{"missing kind", func(s *span.Span) { s.Kind = "" }},
		{"zero start time", func(s *span.Span) { s.StartTime = time.Time{} }},
		{"end before start", func(s *span.Span) { s.EndTime = s.StartTime.Add(-time.Second) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := valid()
			tc.mutate(&s)
			if err := s.Validate(); !errors.Is(err, span.ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCapPayloads(t *testing.T) {
	s := valid()
	s.Input = json.RawMessage(`"small"`)
	if s.CapPayloads(64) {
		t.Fatal("small payload reported as truncated")
	}

	big := json.RawMessage(`"` + strings.Repeat("x", 500) + `"`)
	s.Input = big
	s.Output = big
	if !s.CapPayloads(64) {
		t.Fatal("big payload not reported as truncated")
	}
	if len(s.Input) > 256 || len(s.Output) > 256 {
		t.Fatalf("payloads not capped: input=%d output=%d", len(s.Input), len(s.Output))
	}

	var parsed map[string]any
	if err := json.Unmarshal(s.Input, &parsed); err != nil {
		t.Fatalf("capped payload is not valid JSON: %v", err)
	}
	if parsed["truncated"] != true {
		t.Fatalf("capped payload missing truncated flag: %v", parsed)
	}
	if parsed["original_bytes"] != float64(len(big)) {
		t.Fatalf("original_bytes = %v, want %d", parsed["original_bytes"], len(big))
	}
	if s.Attributes[span.AttrTruncated] != true {
		t.Fatal("span not flagged as truncated")
	}
}

func TestCapPayloadsZeroMaxIsNoop(t *testing.T) {
	s := valid()
	s.Input = json.RawMessage(`"` + strings.Repeat("x", 500) + `"`)
	if s.CapPayloads(0) {
		t.Fatal("max=0 should disable capping")
	}
}

func TestDuration(t *testing.T) {
	s := valid()
	if got := s.Duration(); got != time.Millisecond {
		t.Fatalf("Duration() = %v, want 1ms", got)
	}
	s.EndTime = time.Time{}
	if got := s.Duration(); got != 0 {
		t.Fatalf("Duration() with zero end = %v, want 0", got)
	}
}
