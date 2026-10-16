package demoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

// Config configures the demo agent.
type Config struct {
	// Provider is the LLM backend. Required.
	Provider llm.Provider
	// Tracer records the run. Required.
	Tracer *tracer.Tracer
	// Tools defaults to DefaultRegistry.
	Tools *Registry
	// Model is recorded on llm.call spans.
	Model string
	// MaxSteps bounds the ReAct loop. Defaults to 8.
	MaxSteps int
}

// Agent is a small ReAct research agent: it thinks with an LLM, calls
// tools, and repeats until the model produces a final answer.
type Agent struct {
	provider llm.Provider
	tracer   *tracer.Tracer
	tools    *Registry
	model    string
	maxSteps int
}

// New returns an Agent.
func New(cfg Config) *Agent {
	tools := cfg.Tools
	if tools == nil {
		tools = DefaultRegistry()
	}
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	return &Agent{
		provider: cfg.Provider,
		tracer:   cfg.Tracer,
		tools:    tools,
		model:    cfg.Model,
		maxSteps: maxSteps,
	}
}

// Result describes a finished run.
type Result struct {
	Answer  string
	TraceID string
	Steps   int
}

// action is the JSON protocol the model responds with.
type action struct {
	Thought string          `json:"thought,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Final   string          `json:"final_answer,omitempty"`
}

type stepResult struct {
	Answer     string
	RawAction  string
	ToolResult string
}

// Run answers question with the ReAct loop, recording a trace.
func (a *Agent) Run(ctx context.Context, question string) (Result, error) {
	ctx, run := a.tracer.StartRun(ctx, "research-agent")
	defer run.End()

	run.SetAttribute(span.AttrAgentName, "research-agent")
	_ = run.SetInput(map[string]any{"question": question})

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: a.systemPrompt()},
		{Role: llm.RoleUser, Content: question},
	}

	for step := 1; step <= a.maxSteps; step++ {
		res, err := a.step(ctx, step, messages)
		if err != nil {
			run.RecordError(err)
			return Result{TraceID: run.TraceID()}, err
		}
		if res.Answer != "" {
			_ = run.SetOutput(map[string]any{"answer": res.Answer, "steps": step})
			return Result{Answer: res.Answer, TraceID: run.TraceID(), Steps: step}, nil
		}
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Content: res.RawAction},
			llm.Message{Role: llm.RoleTool, Content: res.ToolResult},
		)
	}

	err := fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps)
	run.RecordError(err)
	return Result{TraceID: run.TraceID(), Steps: a.maxSteps}, err
}

func (a *Agent) step(ctx context.Context, n int, messages []llm.Message) (stepResult, error) {
	_, stepSpan := a.tracer.Start(ctx, fmt.Sprintf("step-%d", n), span.KindAgentStep)
	defer stepSpan.End()

	act, raw, err := a.decide(ctx, messages)
	if err != nil {
		stepSpan.EndWithError(err)
		return stepResult{}, err
	}
	if act.Final != "" {
		return stepResult{Answer: act.Final}, nil
	}

	out, err := a.callTool(ctx, act)
	if err != nil {
		stepSpan.EndWithError(err)
		return stepResult{}, err
	}
	return stepResult{RawAction: raw, ToolResult: out}, nil
}

func (a *Agent) decide(ctx context.Context, messages []llm.Message) (action, string, error) {
	_, llmSpan := a.tracer.StartLLMCall(ctx, "decide")
	llmSpan.SetAttribute(span.AttrModel, a.model)

	resp, err := a.provider.Complete(ctx, llm.Request{Model: a.model, Messages: messages})
	if err != nil {
		llmSpan.EndWithError(err)
		return action{}, "", err
	}

	llmSpan.SetAttribute(span.AttrInputTokens, resp.Usage.InputTokens)
	llmSpan.SetAttribute(span.AttrOutputTokens, resp.Usage.OutputTokens)
	llmSpan.SetAttribute(span.AttrCostUSD, resp.Usage.CostUSD)
	_ = llmSpan.SetInput(messages)
	_ = llmSpan.SetOutput(resp.Content)
	llmSpan.End()

	var act action
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &act); err != nil {
		return action{}, resp.Content, fmt.Errorf("agent: model returned invalid action JSON: %w (content: %.200s)", err, resp.Content)
	}
	if act.Final == "" && act.Tool == "" {
		return action{}, resp.Content, fmt.Errorf("agent: model action has neither tool nor final_answer")
	}
	return act, resp.Content, nil
}

func (a *Agent) callTool(ctx context.Context, act action) (string, error) {
	_, toolSpan := a.tracer.StartToolCall(ctx, act.Tool)
	toolSpan.SetAttribute(span.AttrToolName, act.Tool)
	_ = toolSpan.SetInput(act.Args)

	t, ok := a.tools.Get(act.Tool)
	if !ok {
		err := fmt.Errorf("agent: unknown tool %q", act.Tool)
		toolSpan.EndWithError(err)
		return "", err
	}
	out, err := t.Run(ctx, act.Args)
	if err != nil {
		toolSpan.EndWithError(err)
		return "", err
	}
	_ = toolSpan.SetOutput(out)
	toolSpan.End()
	return out, nil
}

func (a *Agent) systemPrompt() string {
	return fmt.Sprintf(`You are a research agent. Solve the user's question step by step.

Available tools:
%s
Respond with exactly one JSON object and nothing else.

To use a tool: {"thought": "<why>", "tool": "<tool name>", "args": {...}}
When finished: {"thought": "<why>", "final_answer": "<answer>"}`, a.tools.PromptList())
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
