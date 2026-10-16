// Command demoagent runs the Blackbox demo: a ReAct research agent with a
// mock LLM and mock tools, instrumented with the Blackbox tracer SDK.
//
// Start a server first (blackbox serve), then run:
//
//	go run ./cmd/demoagent
//
// Real providers are available via -provider openai|anthropic, which read
// OPENAI_API_KEY / ANTHROPIC_API_KEY from the environment.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/tracer"
)

var demoQuestions = []string{
	"What is the capital of France and its population?",
	"What is 123 * 456?",
	"Tell me about LLM agents.",
}

func main() {
	endpoint := flag.String("endpoint", tracer.DefaultEndpoint, "Blackbox ingest endpoint")
	providerName := flag.String("provider", "mock", "LLM provider: mock, openai, or anthropic")
	model := flag.String("model", "", "model name (provider default when empty)")
	question := flag.String("question", "", "ask a single question instead of the demo set")
	flag.Parse()

	provider, defaultModel, err := buildProvider(*providerName)
	if err != nil {
		log.Fatal(err)
	}
	if *model == "" {
		*model = defaultModel
	}

	tr := tracer.New(tracer.Config{Endpoint: *endpoint, ServiceName: "demoagent"})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tr.Shutdown(ctx); err != nil {
			log.Printf("tracer shutdown: %v", err)
		}
	}()

	agent := demoagent.New(demoagent.Config{Provider: provider, Tracer: tr, Model: *model})

	questions := demoQuestions
	if *question != "" {
		questions = []string{*question}
	}

	ctx := context.Background()
	for _, q := range questions {
		res, err := agent.Run(ctx, q)
		if err != nil {
			fmt.Printf("Q: %s\n  error: %v (trace %s)\n\n", q, err, res.TraceID)
			continue
		}
		fmt.Printf("Q: %s\nA: %s\n  steps=%d trace=%s\n\n", q, res.Answer, res.Steps, res.TraceID)
	}
}

func buildProvider(name string) (llm.Provider, string, error) {
	switch name {
	case "mock":
		return llm.NewMock(), "mock-1", nil
	case "openai":
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, "", fmt.Errorf("OPENAI_API_KEY is required for -provider openai")
		}
		return &llm.OpenAI{APIKey: key}, "gpt-4o-mini", nil
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, "", fmt.Errorf("ANTHROPIC_API_KEY is required for -provider anthropic")
		}
		return &llm.Anthropic{APIKey: key}, "claude-3-5-haiku-latest", nil
	default:
		return nil, "", fmt.Errorf("unknown provider %q (want mock, openai, or anthropic)", name)
	}
}
