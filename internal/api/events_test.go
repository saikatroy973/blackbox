package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/saikat/blackbox/internal/api"
	"github.com/saikat/blackbox/internal/events"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/span"
)

func TestEventStream(t *testing.T) {
	st, err := store.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	hub := events.NewHub()
	mux := http.NewServeMux()
	api.New(st, api.WithEventStream(hub)).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/v1/events")
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	if hub.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", hub.Subscribers())
	}

	lines := make(chan string, 16)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	waitLine := func(want string) {
		t.Helper()
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed waiting for %q", want)
				}
				if line == want {
					return
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("timeout waiting for %q", want)
			}
		}
	}

	waitLine(": connected")

	now := time.Now()
	hub.Publish(span.Span{
		TraceID: "trace-live", SpanID: "s1", Name: "search", Kind: span.KindToolCall,
		StartTime: now, EndTime: now.Add(25 * time.Millisecond), Status: span.StatusOK,
	})

	waitLine("event: span")

	var data string
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream closed waiting for data")
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
				break
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for data line")
		}
		if data != "" {
			break
		}
	}

	var event struct {
		TraceID    string `json:"trace_id"`
		SpanID     string `json:"span_id"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Status     string `json:"status"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatalf("decode event %q: %v", data, err)
	}
	if event.TraceID != "trace-live" || event.Name != "search" || event.Kind != "tool.call" ||
		event.Status != "ok" || event.DurationMS != 25 {
		t.Fatalf("event = %+v", event)
	}
}

func TestEventStreamSubscriberCleanup(t *testing.T) {
	hub := events.NewHub()
	mux := http.NewServeMux()
	api.New(nil, api.WithEventStream(hub)).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if hub.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", hub.Subscribers())
	}
	cancel()
	_ = resp.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for hub.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("subscriber not cleaned up, count = %d", hub.Subscribers())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEventsRouteAbsentWithoutHub(t *testing.T) {
	st, h := seed(t)
	_ = st
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
