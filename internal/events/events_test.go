package events

import (
	"sync"
	"testing"
	"time"

	"github.com/saikat/blackbox/pkg/span"
)

func TestHubFanOut(t *testing.T) {
	h := NewHub()
	a, cancelA := h.Subscribe(4)
	b, cancelB := h.Subscribe(4)
	defer cancelA()
	defer cancelB()

	if h.Subscribers() != 2 {
		t.Fatalf("subscribers = %d, want 2", h.Subscribers())
	}

	h.Publish(span.Span{TraceID: "t1", SpanID: "s1"})
	for _, ch := range []<-chan span.Span{a, b} {
		select {
		case s := <-ch:
			if s.TraceID != "t1" || s.SpanID != "s1" {
				t.Fatalf("span = %+v", s)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive the span")
		}
	}
}

func TestHubCancel(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe(1)
	cancel()
	cancel() // idempotent
	if h.Subscribers() != 0 {
		t.Fatalf("subscribers = %d, want 0", h.Subscribers())
	}
	// Publishing with no subscribers must not panic.
	h.Publish(span.Span{TraceID: "t"})
}

func TestHubSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe(1)
	defer cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Publish(span.Span{TraceID: "t"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscriber")
	}
}

func TestHubConcurrentSubscribePublish(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.Publish(span.Span{TraceID: "t"})
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		ch, cancel := h.Subscribe(2)
		cancel()
		_ = ch
	}
	close(stop)
	wg.Wait()
	// Notify is the ingest hook; make sure it works too.
	h.Notify(span.Span{TraceID: "t"})
}
