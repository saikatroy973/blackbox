package llm

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// Mock prices per 1M tokens, applied to estimated usage.
const (
	mockPricePerMInput  = 0.50
	mockPricePerMOutput = 1.50
)

// Mock is a deterministic model for demos, tests, and replays. It never
// performs I/O and always returns the same response for the same
// conversation.
type Mock struct{}

// NewMock returns a Mock provider.
func NewMock() *Mock { return &Mock{} }

// Name implements Provider.
func (*Mock) Name() string { return "mock" }

var arithmeticRe = regexp.MustCompile(`-?\d+(?:\.\d+)?\s*[+\-*/]\s*-?\d+(?:\.\d+)?`)

// Complete implements Provider.
func (*Mock) Complete(_ context.Context, req Request) (Response, error) {
	var (
		question    string
		toolOutputs []string
		lastTool    string
	)
	for _, m := range req.Messages {
		switch m.Role {
		case RoleUser:
			question = m.Content
		case RoleAssistant:
			if t := toolNameFromAction(m.Content); t != "" {
				lastTool = t
			}
		case RoleTool:
			toolOutputs = append(toolOutputs, m.Content)
		}
	}

	var content string
	if len(toolOutputs) > 0 {
		content = mockFinal(lastTool, toolOutputs)
	} else {
		content = mockFirst(question)
	}

	usage := Usage{
		InputTokens:  estimateTokens(req.Messages),
		OutputTokens: estimateTokens([]Message{{Content: content}}),
	}
	usage.CostUSD = computeCost(usage.InputTokens, usage.OutputTokens, mockPricePerMInput, mockPricePerMOutput)
	return Response{Content: content, Usage: usage}, nil
}

func mockFirst(question string) string {
	if expr := arithmeticRe.FindString(question); expr != "" {
		return toolAction(
			"The question contains an arithmetic expression, so I will use the calculator.",
			"calculator",
			map[string]string{"expression": expr},
		)
	}
	return toolAction(
		"I should search the web for this question.",
		"web_search",
		map[string]string{"query": question},
	)
}

func mockFinal(tool string, outputs []string) string {
	last := outputs[len(outputs)-1]
	switch tool {
	case "calculator":
		return finalAction("The calculator returned a result.", "The answer is "+valueAfterEquals(last)+".")
	case "summarize":
		return finalAction("The text has been summarized.", last)
	default:
		return finalAction("I have enough information from the search results.", "Based on my research: "+last)
	}
}

func toolAction(thought, tool string, args any) string {
	b, _ := json.Marshal(map[string]any{"thought": thought, "tool": tool, "args": args})
	return string(b)
}

func finalAction(thought, answer string) string {
	b, _ := json.Marshal(map[string]any{"thought": thought, "final_answer": answer})
	return string(b)
}

func toolNameFromAction(content string) string {
	var a struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &a); err != nil {
		return ""
	}
	return a.Tool
}

func valueAfterEquals(s string) string {
	if i := strings.LastIndex(s, "="); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return strings.TrimSpace(s)
}

func estimateTokens(messages []Message) int {
	bytes := 0
	for _, m := range messages {
		bytes += len(m.Content)
	}
	return bytes/4 + 1
}
