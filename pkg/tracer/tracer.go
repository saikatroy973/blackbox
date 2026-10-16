// Package tracer is the Blackbox Go SDK: agent authors use it to record
// spans and to export them to a Blackbox server.
package tracer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

// Defaults applied by New when the corresponding Config field is zero.
const (
	DefaultEndpoint      = "http://127.0.0.1:8080/v1/spans"
	DefaultBatchSize     = 64
	DefaultFlushInterval = 2 * time.Second
)

// Config configures a Tracer.
type Config struct {
	// Endpoint of the Blackbox ingest API. Defaults to DefaultEndpoint.
	Endpoint string
	// ServiceName is recorded as the service.name attribute on run spans.
	ServiceName string
	// BatchSize is the number of queued spans that triggers a flush.
	// Defaults to DefaultBatchSize.
	BatchSize int
	// FlushInterval is the maximum time spans sit in the queue.
	// Defaults to DefaultFlushInterval.
	FlushInterval time.Duration
	// HTTPClient used for exports. Defaults to a client with a 5s timeout.
	HTTPClient *http.Client
	// OnError, if set, is called when an export fails. Failed batches are
	// dropped, not retried.
	OnError func(error)
}

// Tracer records spans and exports them to the Blackbox server in batches.
type Tracer struct {
	cfg Config

	mu    sync.Mutex
	queue []span.Span

	flushReq chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New starts a Tracer and its background flush loop. Call Shutdown to
// flush remaining spans and stop it.
func New(cfg Config) *Tracer {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}

	t := &Tracer{
		cfg:      cfg,
		flushReq: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	t.wg.Add(1)
	go t.loop()
	return t
}

// Span is an in-progress span. It must be ended with End or EndWithError.
type Span struct {
	tracer *Tracer
	mu     sync.Mutex
	s      span.Span
	ended  bool
}

// Start begins a span. If ctx carries an active span the new span becomes
// its child; otherwise it becomes the root of a new trace.
func (t *Tracer) Start(ctx context.Context, name string, kind span.Kind) (context.Context, *Span) {
	var traceID, parentID string
	if p := FromContext(ctx); p != nil {
		p.mu.Lock()
		traceID, parentID = p.s.TraceID, p.s.SpanID
		p.mu.Unlock()
	}
	if traceID == "" {
		traceID = newID()
	}

	s := &Span{
		tracer: t,
		s: span.Span{
			TraceID:      traceID,
			SpanID:       newID(),
			ParentSpanID: parentID,
			Name:         name,
			Kind:         kind,
			StartTime:    time.Now(),
			Status:       span.StatusOK,
		},
	}
	if kind == span.KindAgentRun && t.cfg.ServiceName != "" {
		s.SetAttribute(span.AttrServiceName, t.cfg.ServiceName)
	}
	return withSpan(ctx, s), s
}

// StartRun begins a new trace and returns its root span.
func (t *Tracer) StartRun(ctx context.Context, name string) (context.Context, *Span) {
	return t.Start(ctx, name, span.KindAgentRun)
}

// StartLLMCall begins an llm.call span child of the active span.
func (t *Tracer) StartLLMCall(ctx context.Context, name string) (context.Context, *Span) {
	return t.Start(ctx, name, span.KindLLMCall)
}

// StartToolCall begins a tool.call span child of the active span.
func (t *Tracer) StartToolCall(ctx context.Context, name string) (context.Context, *Span) {
	return t.Start(ctx, name, span.KindToolCall)
}

// SetAttribute sets an attribute on the span.
func (s *Span) SetAttribute(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s.SetAttribute(key, value)
}

// TraceID returns the span's trace ID.
func (s *Span) TraceID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.TraceID
}

// SpanID returns the span's ID.
func (s *Span) SpanID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s.SpanID
}

// SetInput records the span's input payload. Raw JSON and []byte values
// are recorded as-is; anything else is JSON-marshaled.
func (s *Span) SetInput(v any) error {
	raw, err := toRaw(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.s.Input = raw
	s.mu.Unlock()
	return nil
}

// SetOutput records the span's output payload, with the same conversion
// rules as SetInput.
func (s *Span) SetOutput(v any) error {
	raw, err := toRaw(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.s.Output = raw
	s.mu.Unlock()
	return nil
}

// RecordError marks the span as failed.
func (s *Span) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.s.Status = span.StatusError
	s.s.Error = err.Error()
	s.mu.Unlock()
}

// End finishes the span and queues it for export. Calling End more than
// once is a no-op.
func (s *Span) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	if s.s.EndTime.IsZero() {
		s.s.EndTime = time.Now()
	}
	if s.s.Status == "" {
		s.s.Status = span.StatusOK
	}
	sp := s.s
	s.mu.Unlock()
	s.tracer.record(sp)
}

// EndWithError records err (if non-nil) and ends the span.
func (s *Span) EndWithError(err error) {
	s.RecordError(err)
	s.End()
}

func toRaw(v any) (json.RawMessage, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return x, nil
	case []byte:
		return json.RawMessage(x), nil
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return nil, fmt.Errorf("marshal span payload: %w", err)
		}
		return b, nil
	}
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type ctxKey struct{}

func withSpan(ctx context.Context, s *Span) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the active span in ctx, or nil.
func FromContext(ctx context.Context) *Span {
	s, _ := ctx.Value(ctxKey{}).(*Span)
	return s
}
