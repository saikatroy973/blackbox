// Package ingest implements the HTTP endpoint that receives span batches
// from tracer SDKs.
package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

// DefaultMaxPayload is the per-payload cap applied to span inputs and
// outputs before they are stored.
const DefaultMaxPayload = 256 << 10 // 256 KiB

// maxBodyBytes caps the size of a single ingest request.
const maxBodyBytes = 32 << 20 // 32 MiB

// Notifier receives spans after they are durably stored. Implementations
// must not block; the events hub drops spans for slow subscribers.
type Notifier interface {
	Notify(span.Span)
}

// Option configures a Handler.
type Option func(*Handler)

// WithNotifier publishes every stored span to n.
func WithNotifier(n Notifier) Option {
	return func(h *Handler) { h.notifier = n }
}

// Handler receives span batches and persists them.
type Handler struct {
	store      store.Store
	maxPayload int
	notifier   Notifier
}

// New returns a Handler that writes to st.
func New(st store.Store, opts ...Option) *Handler {
	h := &Handler{store: st, maxPayload: DefaultMaxPayload}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

type batchRequest struct {
	Spans []span.Span `json:"spans"`
}

type response struct {
	Accepted int `json:"accepted"`
}

// ServeHTTP accepts {"spans": [...]} batches.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var req batchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(req.Spans) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no spans in request")
		return
	}
	for i := range req.Spans {
		req.Spans[i].CapPayloads(h.maxPayload)
		if err := req.Spans[i].Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("span %d: %s", i, err))
			return
		}
	}
	if err := h.store.InsertSpans(r.Context(), req.Spans); err != nil {
		writeError(w, http.StatusInternalServerError, "store: "+err.Error())
		return
	}
	if h.notifier != nil {
		for i := range req.Spans {
			h.notifier.Notify(req.Spans[i])
		}
	}
	writeJSON(w, http.StatusAccepted, response{Accepted: len(req.Spans)})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
