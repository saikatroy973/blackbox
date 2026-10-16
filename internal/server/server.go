// Package server wires the Blackbox HTTP API.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/saikat/blackbox/internal/ingest"
	"github.com/saikat/blackbox/internal/store"
)

// Config configures the HTTP handler.
type Config struct {
	// Version reported by the root endpoint.
	Version string
	// Store backing the ingest endpoint. Required.
	Store store.Store
}

// New returns the root HTTP handler.
func New(cfg Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"name":    "blackbox",
			"version": cfg.Version,
			"tagline": "flight recorder for AI agents",
		})
	})

	if cfg.Store != nil {
		mux.Handle("POST /v1/spans", ingest.New(cfg.Store))
	}
	return mux
}
