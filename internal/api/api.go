// Package api exposes JSON endpoints over the trace store.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/saikat/blackbox/internal/store"
)

// Handler serves the JSON API.
type Handler struct {
	store store.Store
}

// New returns a Handler backed by st.
func New(st store.Store) *Handler {
	return &Handler{store: st}
}

// Routes registers the API on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/traces", h.listTraces)
	mux.HandleFunc("GET /v1/traces/{id}", h.getTrace)
}

func (h *Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.TraceFilter{}

	switch q.Get("status") {
	case "", "ok", "error":
		f.Status = q.Get("status")
	default:
		writeError(w, http.StatusBadRequest, `status must be one of "", "ok", "error"`)
		return
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 500")
			return
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
			return
		}
		f.Offset = n
	}

	traces, err := h.store.ListTraces(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if traces == nil {
		traces = []store.TraceSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": traces})
}

func (h *Handler) getTrace(w http.ResponseWriter, r *http.Request) {
	spans, err := h.store.GetTrace(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"spans": spans})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
