// Package events provides an in-process pub/sub hub that fans stored
// spans out to live subscribers (the SSE endpoint and dashboard).
package events

import (
	"sync"

	"github.com/saikat/blackbox/pkg/span"
)

// Hub fans spans out to subscribers. Slow subscribers are skipped rather
// than blocking ingest.
type Hub struct {
	mu   sync.RWMutex
	subs map[chan span.Span]struct{}
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan span.Span]struct{})}
}

// Subscribe registers a subscriber with the given buffer size and returns
// its channel plus a cancel function. Cancel is idempotent.
func (h *Hub) Subscribe(buffer int) (<-chan span.Span, func()) {
	if buffer <= 0 {
		buffer = 16
	}
	ch := make(chan span.Span, buffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			close(ch)
			h.mu.Unlock()
		})
	}
	return ch, cancel
}

// Publish delivers s to every subscriber without blocking. Subscribers
// whose buffers are full miss the span.
func (h *Hub) Publish(s span.Span) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- s:
		default:
		}
	}
}

// Notify implements ingest's Notifier interface.
func (h *Hub) Notify(s span.Span) { h.Publish(s) }

// Subscribers returns the number of active subscribers.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
