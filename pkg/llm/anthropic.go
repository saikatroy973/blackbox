package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Anthropic is a Provider backed by the Anthropic messages API.
type Anthropic struct {
	APIKey  string
	BaseURL string // defaults to https://api.anthropic.com
	Client  *http.Client

	// MaxTokens defaults to 1024; Anthropic requires the field.
	MaxTokens int

	// PricePerMInput and PricePerMOutput are USD per 1M tokens, used to
	// compute Usage.CostUSD. Leave zero to report no cost.
	PricePerMInput  float64
	PricePerMOutput float64
}

// Name implements Provider.
func (*Anthropic) Name() string { return "anthropic" }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements Provider.
func (a *Anthropic) Complete(ctx context.Context, req Request) (Response, error) {
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	var system []string
	msgs := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case RoleSystem:
			system = append(system, m.Content)
		case RoleTool:
			msgs = append(msgs, anthropicMessage{Role: string(RoleUser), Content: "Tool result: " + m.Content})
		default:
			msgs = append(msgs, anthropicMessage{Role: string(m.Role), Content: m.Content})
		}
	}

	body, err := json.Marshal(anthropicRequest{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		System:      strings.Join(system, "\n\n"),
		Messages:    msgs,
		Temperature: req.Temperature,
	})
	if err != nil {
		return Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return Response{}, apiError("anthropic", httpResp)
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&parsed); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	var content strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}

	usage := Usage{
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}
	usage.CostUSD = computeCost(usage.InputTokens, usage.OutputTokens, a.PricePerMInput, a.PricePerMOutput)
	return Response{Content: content.String(), Usage: usage}, nil
}
