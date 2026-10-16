package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/pkg/llm"
)

// Divergence describes a replayed request that does not match any
// recorded cassette.
type Divergence struct {
	Kind   string // "llm" or "tool"
	Name   string
	Detail string
}

// Logger collects cassette hits and divergences during a replay.
type Logger struct {
	mu      sync.Mutex
	hits    int
	entries []Divergence
}

// NewLogger returns an empty Logger.
func NewLogger() *Logger { return &Logger{} }

func (l *Logger) hit() {
	l.mu.Lock()
	l.hits++
	l.mu.Unlock()
}

func (l *Logger) add(d Divergence) {
	l.mu.Lock()
	l.entries = append(l.entries, d)
	l.mu.Unlock()
}

// Hits returns the number of cassette hits.
func (l *Logger) Hits() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hits
}

// Divergences returns a copy of the recorded divergences.
func (l *Logger) Divergences() []Divergence {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Divergence, len(l.entries))
	copy(out, l.entries)
	return out
}

// Provider serves LLM responses from a cassette set.
type Provider struct {
	set *CassetteSet
	log *Logger
}

// NewProvider returns a replaying Provider.
func NewProvider(set *CassetteSet, log *Logger) *Provider {
	return &Provider{set: set, log: log}
}

// Name implements llm.Provider.
func (*Provider) Name() string { return "replay" }

// Complete implements llm.Provider. It fails with a divergence error when
// the request does not match a recorded cassette.
func (p *Provider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	c, ok := p.set.Get(KeyForLLM(req.Model, req.Messages))
	if !ok {
		detail := describeRequest(req)
		p.log.add(Divergence{Kind: "llm", Name: "complete", Detail: detail})
		return llm.Response{}, fmt.Errorf("replay: divergence — no recorded LLM call matches model=%q (%s)", req.Model, detail)
	}
	p.log.hit()

	if c.Err != "" {
		return llm.Response{}, fmt.Errorf("replay: recorded LLM error: %s", c.Err)
	}
	var content string
	if len(c.Response) > 0 {
		if err := json.Unmarshal(c.Response, &content); err != nil {
			content = string(c.Response)
		}
	}
	return llm.Response{Content: content, Usage: c.Usage}, nil
}

// Tool wraps a real tool, serving recorded outputs from cassettes.
type Tool struct {
	original demoagent.Tool
	set      *CassetteSet
	log      *Logger
}

// Name implements demoagent.Tool.
func (t *Tool) Name() string { return t.original.Name() }

// Description implements demoagent.Tool.
func (t *Tool) Description() string { return t.original.Description() }

// Run implements demoagent.Tool. It fails with a divergence error when
// the call does not match a recorded cassette.
func (t *Tool) Run(_ context.Context, args json.RawMessage) (string, error) {
	c, ok := t.set.Get(KeyForTool(t.Name(), args))
	if !ok {
		detail := "args: " + truncate(string(args), 120)
		t.log.add(Divergence{Kind: "tool", Name: t.Name(), Detail: detail})
		return "", fmt.Errorf("replay: divergence — no recorded call to tool %q matches %s", t.Name(), detail)
	}
	t.log.hit()

	if c.Err != "" {
		return "", errors.New(c.Err)
	}
	var out string
	if len(c.Response) > 0 {
		if err := json.Unmarshal(c.Response, &out); err != nil {
			out = string(c.Response)
		}
	}
	return out, nil
}

// ToolRegistry wraps every tool of the original registry so that calls
// are served from the cassette set.
func ToolRegistry(original *demoagent.Registry, set *CassetteSet, log *Logger) *demoagent.Registry {
	tools := make([]demoagent.Tool, 0, len(original.List()))
	for _, t := range original.List() {
		tools = append(tools, &Tool{original: t, set: set, log: log})
	}
	return demoagent.NewRegistry(tools...)
}

func describeRequest(req llm.Request) string {
	var last string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != llm.RoleSystem {
			last = req.Messages[i].Content
			break
		}
	}
	return fmt.Sprintf("%d messages; last: %q", len(req.Messages), truncate(last, 80))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
