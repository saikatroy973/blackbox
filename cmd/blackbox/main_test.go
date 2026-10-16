package main

import (
	"context"
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
