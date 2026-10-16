package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/saikat/blackbox/internal/store"
)

// TestPostgresConformance runs the shared Store contract against a real
// Postgres instance. Set TEST_POSTGRES_DSN to enable it, e.g.
//
//	TEST_POSTGRES_DSN='postgres://blackbox:blackbox@localhost:5433/blackbox?sslmode=disable' go test ./internal/store/
func TestPostgresConformance(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres conformance test")
	}
	st, err := store.OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer func() { _ = st.Close() }()
	runStoreConformance(t, st)
}

func TestPostgresMigrationsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres migration test")
	}
	for i := 0; i < 2; i++ {
		st, err := store.OpenPostgres(context.Background(), dsn)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
}
