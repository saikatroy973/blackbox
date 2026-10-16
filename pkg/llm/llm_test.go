package llm_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/saikat/blackbox/pkg/llm"
)

func TestMockPicksCalculatorForArithmetic(t *testing.T) {
	resp, err := llm.NewMock().Complete(context.Background(), llm.Request{
		Model:    "mock-1",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "What is 123 * 456?"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var act struct {
		Tool string `json:"tool"`
		Args struct {
			Expression string `json:"expression"`
		} `json:"args"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &act); err != nil {
		t.Fatalf("action is not JSON: %v (%s)", err, resp.Content)
	}
	if act.Tool != "calculator" || act.Args.Expression != "123 * 456" {
		t.Fatalf("action = %+v, want calculator with expression", act)
	}
	if resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens == 0 || resp.Usage.CostUSD <= 0 {
		t.Fatalf("usage = %+v, want nonzero tokens and cost", resp.Usage)
	}
}

func TestMockSearchThenFinalAnswer(t *testing.T) {
	mock := llm.NewMock()
	question := "What is the capital of France and its population?"

	first, err := mock.Complete(context.Background(), llm.Request{
		Model:    "mock-1",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: question}},
	})
	if err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	var act struct {
		Tool string `json:"tool"`
		Args struct {
			Query string `json:"query"`
		} `json:"args"`
	}
	if err := json.Unmarshal([]byte(first.Content), &act); err != nil {
		t.Fatalf("first action is not JSON: %v", err)
	}
	if act.Tool != "web_search" || act.Args.Query != question {
		t.Fatalf("first action = %+v, want web_search with the question", act)
	}

	second, err := mock.Complete(context.Background(), llm.Request{
		Model: "mock-1",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: question},
			{Role: llm.RoleAssistant, Content: first.Content},
			{Role: llm.RoleTool, Content: "Paris is the capital of France. City population: about 2.1 million."},
		},
	})
	if err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	var fin struct {
		Final string `json:"final_answer"`
	}
	if err := json.Unmarshal([]byte(second.Content), &fin); err != nil {
		t.Fatalf("final action is not JSON: %v", err)
	}
	if !strings.Contains(fin.Final, "Paris") {
		t.Fatalf("final answer = %q, want it to mention Paris", fin.Final)
	}
}

func TestMockFinalAfterCalculator(t *testing.T) {
	resp, err := llm.NewMock().Complete(context.Background(), llm.Request{
		Model: "mock-1",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "What is 123 * 456?"},
			{Role: llm.RoleAssistant, Content: `{"tool": "calculator", "args": {"expression": "123 * 456"}}`},
			{Role: llm.RoleTool, Content: "123 * 456 = 56088"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var fin struct {
		Final string `json:"final_answer"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &fin); err != nil {
		t.Fatalf("action is not JSON: %v", err)
	}
	if !strings.Contains(fin.Final, "56088") {
		t.Fatalf("final answer = %q, want 56088", fin.Final)
	}
}

func TestOpenAIComplete(t *testing.T) {
	var (
		gotPath  string
		gotAuth  string
		gotModel string
		gotMsgs  []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model    string           `json:"model"`
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = body.Model
		gotMsgs = body.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices": [{"message": {"role": "assistant", "content": "hello"}}],
			"usage": {"prompt_tokens": 100, "completion_tokens": 50}
		}`)
	}))
	defer srv.Close()

	o := &llm.OpenAI{APIKey: "test-key", BaseURL: srv.URL, PricePerMInput: 1, PricePerMOutput: 2}
	resp, err := o.Complete(context.Background(), llm.Request{
		Model: "gpt-x",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleTool, Content: "tool out"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotModel != "gpt-x" {
		t.Fatalf("model = %q", gotModel)
	}
	last := gotMsgs[len(gotMsgs)-1]
	if last["role"] != "user" || last["content"] != "Tool result: tool out" {
		t.Fatalf("tool message mapping = %v", last)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if want := 0.0002; math.Abs(resp.Usage.CostUSD-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", resp.Usage.CostUSD, want)
	}
}

func TestOpenAIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error": "bad key"}`)
	}))
	defer srv.Close()

	o := &llm.OpenAI{APIKey: "nope", BaseURL: srv.URL}
	_, err := o.Complete(context.Background(), llm.Request{Model: "gpt-x"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want 401", err)
	}
}

func TestAnthropicComplete(t *testing.T) {
	var (
		gotPath    string
		gotKey     string
		gotVersion string
		gotBody    struct {
			System   string           `json:"system"`
			Messages []map[string]any `json:"messages"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"content": [{"type": "text", "text": "hello "}, {"type": "text", "text": "world"}],
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer srv.Close()

	a := &llm.Anthropic{APIKey: "test-key", BaseURL: srv.URL, PricePerMInput: 3, PricePerMOutput: 15}
	resp, err := a.Complete(context.Background(), llm.Request{
		Model: "claude-x",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "sys"},
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleTool, Content: "tool out"},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if gotPath != "/v1/messages" || gotKey != "test-key" || gotVersion != "2023-06-01" {
		t.Fatalf("path/key/version = %q/%q/%q", gotPath, gotKey, gotVersion)
	}
	if gotBody.System != "sys" {
		t.Fatalf("system = %q, want sys", gotBody.System)
	}
	last := gotBody.Messages[len(gotBody.Messages)-1]
	if last["role"] != "user" || last["content"] != "Tool result: tool out" {
		t.Fatalf("tool message mapping = %v", last)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if want := 0.000105; math.Abs(resp.Usage.CostUSD-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", resp.Usage.CostUSD, want)
	}
}

func TestAnthropicError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error": {"message": "boom"}}`)
	}))
	defer srv.Close()

	a := &llm.Anthropic{APIKey: "k", BaseURL: srv.URL}
	_, err := a.Complete(context.Background(), llm.Request{Model: "claude-x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want 500", err)
	}
}
