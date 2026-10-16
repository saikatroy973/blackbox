// Package api exposes JSON endpoints over the trace store.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/saikat/blackbox/internal/events"
	"github.com/saikat/blackbox/internal/store"
)

// Option configures the API handler.
type Option func(*Handler)

// WithEventStream enables GET /v1/events, a server-sent event stream of
// spans as they are stored.
func WithEventStream(hub *events.Hub) Option {
	return func(h *Handler) { h.events = hub }
}

// Handler serves the JSON API.
type Handler struct {
	store  store.Store
	events *events.Hub
}

// New returns a Handler backed by st.
func New(st store.Store, opts ...Option) *Handler {
	h := &Handler{store: st}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Routes registers the API on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/traces", h.listTraces)
	mux.HandleFunc("GET /v1/traces/{id}", h.getTrace)
	if h.events != nil {
		mux.HandleFunc("GET /v1/events", h.eventStream)
	}
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

// spanEvent is the compact SSE payload for a stored span.
type spanEvent struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

const heartbeatInterval = 15 * time.Second

// eventStream streams stored spans as server-sent events.
func (h *Handler) eventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ch, cancel := h.events.Subscribe(64)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case s, open := <-ch:
			if !open {
				return
			}
			data, err := json.Marshal(spanEvent{
				TraceID:    s.TraceID,
				SpanID:     s.SpanID,
				Name:       s.Name,
				Kind:       string(s.Kind),
				Status:     string(s.Status),
				DurationMS: s.Duration().Milliseconds(),
			})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: span\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
