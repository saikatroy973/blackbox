// Package llm defines a provider-agnostic interface for chat completions,
// with implementations for a deterministic mock model and the OpenAI and
// Anthropic APIs.
package llm

import "context"

// Role identifies the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single turn in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request is a chat completion request.
type Request struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

// Usage reports token consumption and cost of a completion.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// Response is a chat completion response.
type Response struct {
	Content string
	Usage   Usage
}

// Provider completes chat requests. Implementations must be safe for
// concurrent use.
type Provider interface {
	// Complete returns the assistant's reply to req.
	Complete(ctx context.Context, req Request) (Response, error)
	// Name identifies the provider, e.g. "mock".
	Name() string
}

func computeCost(inputTokens, outputTokens int, pricePerMInput, pricePerMOutput float64) float64 {
	return float64(inputTokens)/1e6*pricePerMInput + float64(outputTokens)/1e6*pricePerMOutput
}
