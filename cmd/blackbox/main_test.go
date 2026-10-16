package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/demoagent"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
	"github.com/saikat/blackbox/pkg/tracer"
)

func recordTrace(t *testing.T, db string) string {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenSQLite(ctx, db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	tr := tracer.New(tracer.Config{
		Exporter:      tracer.ExporterFunc(st.InsertSpans),
		ServiceName:   "test",
		BatchSize:     1,
		FlushInterval: time.Hour,
	})
	agent := demoagent.New(demoagent.Config{Provider: llm.NewMock(), Tracer: tr, Model: "mock-1"})
	res, err := agent.Run(ctx, "What is 2 + 3?")
	if err != nil {
		t.Fatalf("run agent: %v", err)
	}
	if err := tr.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	return res.TraceID
}

func TestReplayCommandMatches(t *testing.T) {
	db := filepath.Join(t.TempDir(), "test.db")
	traceID := recordTrace(t, db)

	if err := run([]string{"replay", "-db", db, traceID}); err != nil {
		t.Fatalf("replay command: %v", err)
	}
}

func TestReplayCommandUsage(t *testing.T) {
	if err := run([]string{"replay"}); err == nil {
		t.Fatal("expected usage error")
	}
}

func writeSuite(t *testing.T, question string, expectations string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.yaml")
	doc := "suite: cli-test\ncases:\n  - name: case-1\n    question: \"" + question + "\"\n    expect:\n" + expectations
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return path
}

func TestEvalCommandPassSaveBaseline(t *testing.T) {
	db := filepath.Join(t.TempDir(), "eval.db")
	suite := writeSuite(t, "What is 2 + 3?", `      output_contains: ["5"]`+"\n      tools_called: [\"calculator\"]\n")
	saved := filepath.Join(t.TempDir(), "baseline.json")

	if err := run([]string{"eval", "run", "-suite", suite, "-db", db, "-save", saved}); err != nil {
		t.Fatalf("eval run: %v", err)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("baseline not saved: %v", err)
	}
	if err := run([]string{"eval", "run", "-suite", suite, "-db", db, "-baseline", saved}); err != nil {
		t.Fatalf("eval run with baseline: %v", err)
	}
}

func TestEvalCommandFailsOnBadExpectation(t *testing.T) {
	db := filepath.Join(t.TempDir(), "eval.db")
	suite := writeSuite(t, "What is 2 + 3?", `      output_contains: ["definitely-not-there"]`+"\n")

	err := run([]string{"eval", "run", "-suite", suite, "-db", db})
	if !errors.Is(err, errEvalFailed) {
		t.Fatalf("err = %v, want errEvalFailed", err)
	}
}

func TestEvalCommandUsage(t *testing.T) {
	if err := run([]string{"eval"}); err == nil {
		t.Fatal("expected usage error")
	}
	if err := run([]string{"eval", "run"}); err == nil {
		t.Fatal("expected usage error for missing suite")
	}
}
