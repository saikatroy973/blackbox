// Package replay re-runs recorded traces deterministically against
// cassettes extracted from the original spans.
//
// A cassette is the recorded response of one LLM call or tool call,
// keyed by a hash of the request (model + messages, or tool name + args)
// so that any change to prompts, tools, or inputs shows up as a
// divergence instead of silently producing a different run.
package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
)

// Cassette is one recorded model or tool response.
type Cassette struct {
	Key      string          `json:"key"`
	Kind     string          `json:"kind"` // "llm" or "tool"
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response,omitempty"`
	Err      string          `json:"error,omitempty"`
	Usage    llm.Usage       `json:"usage,omitempty"`
}

// CassetteSet is a lookup table of cassettes keyed by request hash.
type CassetteSet struct {
	cassettes map[string]Cassette
}

// FromSpans extracts cassettes from the llm.call and tool.call spans of a
// recorded trace. Spans without a recorded request payload are skipped.
func FromSpans(spans []span.Span) *CassetteSet {
	set := &CassetteSet{cassettes: make(map[string]Cassette, len(spans))}
	for _, s := range spans {
		switch s.Kind {
		case span.KindLLMCall:
			if len(s.Input) == 0 {
				continue
			}
			var messages []llm.Message
			if err := json.Unmarshal(s.Input, &messages); err != nil {
				continue
			}
			model, _ := s.Attributes[span.AttrModel].(string)
			key := KeyForLLM(model, messages)
			set.cassettes[key] = Cassette{
				Key:      key,
				Kind:     "llm",
				Name:     s.Name,
				Response: s.Output,
				Err:      s.Error,
				Usage:    usageFromAttrs(s.Attributes),
			}
		case span.KindToolCall:
			if len(s.Input) == 0 {
				continue
			}
			key := KeyForTool(s.Name, s.Input)
			set.cassettes[key] = Cassette{
				Key:      key,
				Kind:     "tool",
				Name:     s.Name,
				Response: s.Output,
				Err:      s.Error,
			}
		}
	}
	return set
}

// Get returns the cassette for key.
func (c *CassetteSet) Get(key string) (Cassette, bool) {
	cassette, ok := c.cassettes[key]
	return cassette, ok
}

// Len returns the number of cassettes in the set.
func (c *CassetteSet) Len() int {
	return len(c.cassettes)
}

// KeyForLLM derives the cassette key of an LLM request.
func KeyForLLM(model string, messages []llm.Message) string {
	b, err := json.Marshal(messages)
	if err != nil {
		b = []byte{}
	}
	return hashKey("llm", model, string(b))
}

// KeyForTool derives the cassette key of a tool call. Args are
// canonicalized so whitespace and key order do not matter.
func KeyForTool(name string, args json.RawMessage) string {
	return hashKey("tool", name, canonicalJSON(args))
}

func hashKey(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func canonicalJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func usageFromAttrs(attrs map[string]any) llm.Usage {
	var u llm.Usage
	if v, ok := attrs[span.AttrInputTokens].(float64); ok {
		u.InputTokens = int(v)
	}
	if v, ok := attrs[span.AttrOutputTokens].(float64); ok {
		u.OutputTokens = int(v)
	}
	if v, ok := attrs[span.AttrCostUSD].(float64); ok {
		u.CostUSD = v
	}
	return u
}
