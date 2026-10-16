package audit

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingSink records every event it receives. block, when set, holds
// each Write until it is closed (or the write's context ends).
type recordingSink struct {
	mu     sync.Mutex
	events []Event
	block  chan struct{}
	closed atomic.Bool
}

func (s *recordingSink) Write(ctx context.Context, evt Event) error {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.events = append(s.events, evt)
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) Close() error { s.closed.Store(true); return nil }

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// remoteSink is a recordingSink that the engine treats like an HTTP or
// syslog output: it gets its own queue.
type remoteSink struct{ recordingSink }

func (s *remoteSink) queuedDelivery() {}

func emitN(e *Engine, n int) {
	for i := 0; i < n; i++ {
		e.Emit(Event{Type: EventFileWrite, Path: "/f" + strconv.Itoa(i)})
	}
}

// R3: the file output must never lose an event. A burst far larger than
// the engine queue, against a file sink that is slower than the callers,
// makes Emit wait instead of dropping.
func TestEngineNeverDropsForSynchronousSinks(t *testing.T) {
	file := &recordingSink{block: make(chan struct{})}
	e := NewEngine(nil, file)

	const n = 3 * engineQueueSize
	done := make(chan struct{})
	go func() {
		emitN(e, n)
		close(done)
	}()

	// With the sink stalled, the producer must be held back, not finish by
	// throwing events away.
	select {
	case <-done:
		t.Fatal("Emit returned for all events while the file sink was stalled: events were dropped instead of applying backpressure")
	case <-time.After(200 * time.Millisecond):
	}

	close(file.block)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("producer still blocked after the file sink resumed")
	}
	e.Close()
	if got := file.count(); got != n {
		t.Fatalf("file sink received %d of %d events", got, n)
	}
	for _, st := range e.Stats() {
		if st.Dropped != 0 {
			t.Fatalf("synchronous sink reports %d dropped events", st.Dropped)
		}
	}
}

// R3: a stalled remote collector must not delay the file sink or the
// callers; it overflows its own queue, and only its own events are dropped
// and counted.
func TestEngineRemoteSinkHasItsOwnQueue(t *testing.T) {
	file := &recordingSink{}
	remote := &remoteSink{recordingSink{block: make(chan struct{})}}
	e := NewEngine(nil, file, remote)
	e.drainTimeout = 50 * time.Millisecond

	const n = sinkQueueSize + 500
	start := time.Now()
	emitN(e, n)
	// Every event reaches the file even though the remote sink has not
	// accepted a single one.
	deadline := time.Now().Add(10 * time.Second)
	for file.count() < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := file.count(); got != n {
		t.Fatalf("file sink received %d of %d events while the remote sink was stalled", got, n)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a stalled remote sink slowed the callers down: %v", elapsed)
	}

	stats := e.Stats()
	if len(stats) != 2 {
		t.Fatalf("stats for %d sinks, want 2", len(stats))
	}
	if stats[0].Queued || stats[0].Dropped != 0 || stats[0].Output != 1 {
		t.Fatalf("file sink stats = %+v, want synchronous output 1 with no drops", stats[0])
	}
	r := stats[1]
	if !r.Queued || r.Output != 2 || r.Type != "custom" {
		t.Fatalf("remote sink stats = %+v", r)
	}
	// The worker holds one event in a blocked Write; the queue holds
	// sinkQueueSize more; everything else overflowed.
	if want := uint64(n - sinkQueueSize - 1); r.Dropped != want {
		t.Fatalf("remote sink dropped %d events, want %d", r.Dropped, want)
	}
	if r.QueueLength != sinkQueueSize {
		t.Fatalf("remote queue length %d, want %d", r.QueueLength, sinkQueueSize)
	}

	// Close does not wait forever for a collector that never answers: the
	// drain times out and the undelivered rest is counted as dropped.
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung on a stalled remote sink")
	}
	if got := remote.count(); got != 0 {
		t.Fatalf("stalled remote sink received %d events", got)
	}
	if got := e.Stats()[1].Dropped; got != n {
		t.Fatalf("after Close the remote sink counts %d dropped, want all %d", got, n)
	}
	if !file.closed.Load() || !remote.closed.Load() {
		t.Fatal("Close did not close every sink")
	}
}

// A healthy remote sink receives every event, in order, and Close waits
// for its queue to drain.
func TestEngineRemoteSinkDeliversEverythingInOrder(t *testing.T) {
	file := &recordingSink{}
	remote := &remoteSink{}
	e := NewEngine(nil, file, remote)
	const n = 2000
	emitN(e, n)
	e.Close()
	if file.count() != n || remote.count() != n {
		t.Fatalf("file got %d, remote got %d, want %d each", file.count(), remote.count(), n)
	}
	for i, evt := range remote.events {
		if want := "/f" + strconv.Itoa(i); evt.Path != want {
			t.Fatalf("remote event %d has path %q, want %q (out of order)", i, evt.Path, want)
		}
	}
	if d := e.Stats()[1].Dropped; d != 0 {
		t.Fatalf("healthy remote sink dropped %d", d)
	}
}

// Emitting concurrently with Close neither panics nor deadlocks, and an
// Emit after Close is a no-op.
func TestEngineEmitDuringClose(t *testing.T) {
	file := &recordingSink{}
	e := NewEngine(nil, file)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			emitN(e, 500)
		}()
	}
	time.Sleep(time.Millisecond)
	e.Close()
	wg.Wait()
	before := file.count()
	e.Emit(Event{Type: EventFileWrite})
	if file.count() != before {
		t.Fatal("Emit after Close reached the sink")
	}
}

// The built-in remote sinks get their own queues; the file sink does not.
func TestEngineQueuesOnlyRemoteSinks(t *testing.T) {
	file, err := NewFileSink(t.TempDir() + "/audit.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	httpSink, err := NewHTTPSink(HTTPSinkOptions{URL: "http://127.0.0.1:1/audit"})
	if err != nil {
		t.Fatal(err)
	}
	syslog, err := NewSyslogSink(SyslogSinkOptions{URL: "udp://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(nil, file, httpSink, syslog)
	e.drainTimeout = 50 * time.Millisecond
	defer e.Close()
	want := []struct {
		typ    string
		queued bool
	}{{"file", false}, {"http", true}, {"syslog", true}}
	stats := e.Stats()
	for i, w := range want {
		if stats[i].Type != w.typ || stats[i].Queued != w.queued || stats[i].Output != i+1 {
			t.Fatalf("sink %d stats = %+v, want type %s queued %v", i, stats[i], w.typ, w.queued)
		}
	}
	var nilEngine *Engine
	if nilEngine.Stats() != nil {
		t.Fatal("nil engine must report no stats")
	}
}
