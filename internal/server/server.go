// Package server wires the Blackbox HTTP API.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/saikat/blackbox/internal/api"
	"github.com/saikat/blackbox/internal/ingest"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/internal/ui"
	"github.com/saikat/blackbox/web"
)

// Config configures the HTTP handler.
type Config struct {
	// Version reported by the root endpoint.
	Version string
	// Store backing the API and dashboard.
	Store store.Store
}

// New returns the root HTTP handler.
func New(cfg Config) (http.Handler, error) {
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
		api.New(cfg.Store).Routes(mux)

		uiHandler, err := ui.New(cfg.Store)
		if err != nil {
			return nil, fmt.Errorf("init dashboard: %w", err)
		}
		uiHandler.Routes(mux)
		mux.Handle("GET /static/", http.FileServerFS(web.FS))
	}

	return mux, nil
}
