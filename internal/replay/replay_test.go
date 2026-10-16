package replay_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/internal/replay"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/span"
	"github.com/saikat/blackbox/pkg/tracer"
)

func openStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func recordRun(t *testing.T, st store.Store, question string) string {
	t.Helper()
	tr := tracer.New(tracer.Config{
		Exporter:      tracer.ExporterFunc(st.InsertSpans),
		ServiceName:   "test",
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	agent := demoagent.New(demoagent.Config{Provider: llm.NewMock(), Tracer: tr, Model: "mock-1"})
	res, err := agent.Run(context.Background(), question)
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	return res.TraceID
}

func TestReplayMatchesRecordedRun(t *testing.T) {
	st := openStore(t)
	orig := recordRun(t, st, "What is the capital of France and its population?")

	report, err := replay.New(st).Run(context.Background(), orig)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Misses != 0 || len(report.Divergences) != 0 {
		t.Fatalf("unexpected divergences: %+v", report.Divergences)
	}
	if report.RunErr != nil {
		t.Fatalf("run error: %v", report.RunErr)
	}
	if report.Hits == 0 {
		t.Fatal("no cassette hits recorded")
	}
	if report.ReplayTraceID == "" || report.ReplayTraceID == orig {
		t.Fatalf("replay trace = %q, want a new trace", report.ReplayTraceID)
	}
	if !strings.Contains(report.Answer, "Paris") {
		t.Fatalf("answer = %q", report.Answer)
	}

	original, err := st.GetTrace(context.Background(), orig)
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	replayed, err := st.GetTrace(context.Background(), report.ReplayTraceID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if len(replayed) != len(original) {
		t.Fatalf("replay has %d spans, original has %d", len(replayed), len(original))
	}

	var origAnswer string
	for _, s := range original {
		if s.ParentSpanID == "" {
			var payload struct {
				Answer string `json:"answer"`
			}
			if err := json.Unmarshal(s.Output, &payload); err == nil {
				origAnswer = payload.Answer
			}
		}
	}
	if origAnswer == "" || origAnswer != report.Answer {
		t.Fatalf("answers differ: original %q, replay %q", origAnswer, report.Answer)
	}

	root := replayed[0]
	for _, s := range replayed {
		if s.ParentSpanID == "" {
			root = s
		}
	}
	if got := root.Attributes[span.AttrReplayOf]; got != orig {
		t.Fatalf("replay_of attribute = %v, want %q", got, orig)
	}
}

func TestReplayDivergesOnChangedQuestion(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	orig := recordRun(t, st, "What is the capital of France and its population?")

	spans, err := st.GetTrace(ctx, orig)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	for i := range spans {
		if spans[i].ParentSpanID == "" {
			spans[i].Input = json.RawMessage(`{"question": "What is 999 + 1?"}`)
		}
	}
	if err := st.InsertSpans(ctx, spans); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	report, err := replay.New(st).Run(ctx, orig)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Misses == 0 {
		t.Fatal("expected divergences for a changed question")
	}
	if report.RunErr == nil {
		t.Fatal("expected run error after divergence")
	}
	if report.Divergences[0].Kind != "llm" {
		t.Fatalf("divergence = %+v, want llm divergence", report.Divergences[0])
	}
}

func TestReplayUnknownTrace(t *testing.T) {
	st := openStore(t)
	_, err := replay.New(st).Run(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for missing trace")
	}
}

func TestToolReplayServesRecordedError(t *testing.T) {
	now := time.Now()
	set := replay.FromSpans([]span.Span{{
		TraceID: "t", SpanID: "s", Name: "web_search", Kind: span.KindToolCall,
		StartTime: now, EndTime: now, Status: span.StatusError, Error: "rate limited",
		Input: json.RawMessage(`{"query":"x"}`),
	}})
	log := replay.NewLogger()
	registry := replay.ToolRegistry(demoagent.DefaultRegistry(), set, log)

	tool, ok := registry.Get("web_search")
	if !ok {
		t.Fatal("web_search missing from replay registry")
	}

	// Different whitespace must still match the cassette.
	_, err := tool.Run(context.Background(), json.RawMessage(`{ "query" : "x" }`))
	if err == nil || err.Error() != "rate limited" {
		t.Fatalf("err = %v, want the recorded error", err)
	}
	if log.Hits() != 1 {
		t.Fatalf("hits = %d, want 1", log.Hits())
	}

	_, err = tool.Run(context.Background(), json.RawMessage(`{"query":"different"}`))
	if err == nil || !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("err = %v, want divergence", err)
	}
	if len(log.Divergences()) != 1 {
		t.Fatalf("divergences = %+v, want 1", log.Divergences())
	}

	// Tool descriptions must survive wrapping so replay prompts stay identical.
	if tool.Description() != (demoagent.SearchTool{}).Description() {
		t.Fatal("tool description changed during replay wrapping")
	}
}

func TestKeyForLLMIsStable(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "q"},
	}
	k1 := replay.KeyForLLM("mock-1", messages)
	k2 := replay.KeyForLLM("mock-1", messages)
	if k1 != k2 {
		t.Fatalf("keys differ: %q vs %q", k1, k2)
	}
	if k3 := replay.KeyForLLM("other-model", messages); k3 == k1 {
		t.Fatal("key should include the model")
	}
	changed := []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "q2"}}
	if k4 := replay.KeyForLLM("mock-1", changed); k4 == k1 {
		t.Fatal("key should include message content")
	}
}
