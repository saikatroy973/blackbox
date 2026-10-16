package tracer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

type exportRequest struct {
	Spans []span.Span `json:"spans"`
}

func (t *Tracer) record(s span.Span) {
	t.mu.Lock()
	t.queue = append(t.queue, s)
	pending := len(t.queue)
	t.mu.Unlock()

	if pending >= t.cfg.BatchSize {
		select {
		case t.flushReq <- struct{}{}:
		default:
		}
	}
}

func (t *Tracer) loop() {
	defer t.wg.Done()
	ticker := time.NewTicker(t.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.flush()
		case <-t.flushReq:
			t.flush()
		case <-t.done:
			t.flush()
			return
		}
	}
}

func (t *Tracer) flush() {
	t.mu.Lock()
	if len(t.queue) == 0 {
		t.mu.Unlock()
		return
	}
	batch := t.queue
	t.queue = nil
	t.mu.Unlock()

	if err := t.export(batch); err != nil && t.cfg.OnError != nil {
		t.cfg.OnError(err)
	}
}

func (t *Tracer) export(batch []span.Span) error {
	body, err := json.Marshal(exportRequest{Spans: batch})
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("export: unexpected status %s", resp.Status)
	}
	return nil
}

// Shutdown flushes queued spans and stops the tracer. It is safe to call
// multiple times.
func (t *Tracer) Shutdown(ctx context.Context) error {
	t.stopOnce.Do(func() { close(t.done) })

	waited := make(chan struct{})
	go func() {
		t.wg.Wait()
		close(waited)
	}()

	select {
	case <-waited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
