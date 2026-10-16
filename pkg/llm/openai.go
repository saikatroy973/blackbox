package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAI is a Provider backed by the OpenAI chat completions API.
type OpenAI struct {
	APIKey  string
	BaseURL string // defaults to https://api.openai.com/v1
	Client  *http.Client

	// PricePerMInput and PricePerMOutput are USD per 1M tokens, used to
	// compute Usage.CostUSD. Leave zero to report no cost.
	PricePerMInput  float64
	PricePerMOutput float64
}

// Name implements Provider.
func (*OpenAI) Name() string { return "openai" }

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete implements Provider.
func (o *OpenAI) Complete(ctx context.Context, req Request) (Response, error) {
	base := o.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	msgs := make([]openAIMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := string(m.Role)
		if m.Role == RoleTool {
			role = string(RoleUser)
		}
		content := m.Content
		if m.Role == RoleTool {
			content = "Tool result: " + content
		}
		msgs = append(msgs, openAIMessage{Role: role, Content: content})
	}

	body, err := json.Marshal(openAIChatRequest{
		Model:       req.Model,
		Messages:    msgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)

	httpResp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return Response{}, apiError("openai", httpResp)
	}

	var parsed openAIChatResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&parsed); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("openai: response has no choices")
	}

	usage := Usage{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}
	usage.CostUSD = computeCost(usage.InputTokens, usage.OutputTokens, o.PricePerMInput, o.PricePerMOutput)
	return Response{Content: parsed.Choices[0].Message.Content, Usage: usage}, nil
}

func apiError(provider string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s: unexpected status %s: %s", provider, resp.Status, bytes.TrimSpace(body))
}
