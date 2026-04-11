package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPSinkSendsSingleEvent(t *testing.T) {
	var mu sync.Mutex
	var received Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if got := r.Header.Get("X-Audit-Token"); got != "secret" {
			t.Fatalf("expected audit header, got %q", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sink, err := NewHTTPSink(HTTPSinkOptions{
		URL:     server.URL,
		Method:  http.MethodPost,
		Headers: map[string]string{"X-Audit-Token": "secret"},
	})
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}
	defer sink.Close()

	event := Event{Type: EventAuthSuccess, Username: "alice"}
	if err := sink.Write(context.Background(), event); err != nil {
		t.Fatalf("Write: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if received.Username != "alice" || received.Type != EventAuthSuccess {
		t.Fatalf("unexpected received event: %#v", received)
	}
}

func TestHTTPSinkBatchesEvents(t *testing.T) {
	var mu sync.Mutex
	var received [][]Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []Event
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		mu.Lock()
		received = append(received, batch)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := NewHTTPSink(HTTPSinkOptions{
		URL:       server.URL,
		BatchSize: 2,
	})
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}

	if err := sink.Write(context.Background(), Event{Type: EventAuthSuccess, Username: "alice"}); err != nil {
		t.Fatalf("Write #1: %v", err)
	}
	if err := sink.Write(context.Background(), Event{Type: EventFileWrite, Username: "bob"}); err != nil {
		t.Fatalf("Write #2: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || len(received[0]) != 2 {
		t.Fatalf("unexpected batches: %#v", received)
	}
}

func TestHTTPSinkFlushesOnInterval(t *testing.T) {
	done := make(chan []Event, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []Event
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		done <- batch
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := NewHTTPSink(HTTPSinkOptions{
		URL:           server.URL,
		BatchSize:     10,
		FlushInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHTTPSink: %v", err)
	}
	defer sink.Close()

	if err := sink.Write(context.Background(), Event{Type: EventFileDelete, Username: "carol"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case batch := <-done:
		if len(batch) != 1 || batch[0].Username != "carol" {
			t.Fatalf("unexpected flushed batch: %#v", batch)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected batched event to flush on interval")
	}
}
