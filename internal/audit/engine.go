package audit

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kervanserver/kervan/internal/util/ulid"
)

type Sink interface {
	Write(context.Context, Event) error
	Close() error
}

// queuedSink marks sinks that deliver to a remote collector (HTTP, syslog).
// The engine gives each one its own bounded queue and worker, so a slow or
// unreachable collector can neither delay the file sink nor block Emit. When
// its queue is full, events for that sink alone are dropped and counted.
type queuedSink interface {
	Sink
	queuedDelivery()
}

const (
	// engineQueueSize bounds the events waiting for the synchronous sinks.
	// When it is full Emit blocks (backpressure) instead of dropping: the
	// file sink is the record of truth.
	engineQueueSize = 1024
	// sinkQueueSize bounds each remote sink's own queue.
	sinkQueueSize = 4096
	// sinkDrainTimeout bounds how long Close waits for remote sinks to
	// deliver what is still queued; the rest is counted as dropped.
	sinkDrainTimeout = 5 * time.Second
)

// SinkStats describes one configured sink, in configuration order.
type SinkStats struct {
	// Output is the sink's position among the configured outputs (1-based).
	Output int
	// Type is "file", "http", "syslog" or "custom".
	Type string
	// Queued reports whether the sink has its own queue (remote sinks).
	Queued bool
	// QueueLength is the number of events waiting in the sink's queue.
	QueueLength int
	// Dropped counts events this sink never received because its queue was
	// full or the engine was shutting down. Always 0 for synchronous sinks.
	Dropped uint64
}

type sinkWorker struct {
	sink    Sink
	output  int
	kind    string
	ch      chan Event // nil for synchronous sinks
	dropped atomic.Uint64
}

type Engine struct {
	onWrite atomic.Pointer[func()]
	logger  *slog.Logger
	ch      chan Event
	sync    []*sinkWorker
	queued  []*sinkWorker
	all     []*sinkWorker
	wg      sync.WaitGroup // the main loop
	qwg     sync.WaitGroup // remote sink workers
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
	closed  bool

	drainTimeout time.Duration
}

func NewEngine(logger *slog.Logger, sinks ...Sink) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		logger:       logger,
		ch:           make(chan Event, engineQueueSize),
		ctx:          ctx,
		cancel:       cancel,
		drainTimeout: sinkDrainTimeout,
	}
	for i, sink := range sinks {
		w := &sinkWorker{sink: sink, output: i + 1, kind: sinkKind(sink)}
		if _, ok := sink.(queuedSink); ok {
			w.ch = make(chan Event, sinkQueueSize)
			e.queued = append(e.queued, w)
			e.qwg.Add(1)
			go e.runQueued(w)
		} else {
			e.sync = append(e.sync, w)
		}
		e.all = append(e.all, w)
	}
	e.wg.Add(1)
	go e.loop()
	return e
}

func sinkKind(s Sink) string {
	switch s.(type) {
	case *FileSink:
		return "file"
	case *HTTPSink:
		return "http"
	case *SyslogSink:
		return "syslog"
	default:
		return "custom"
	}
}

// Emit queues an event. It never drops: when the queue is full it waits
// until the synchronous sinks (the audit file) catch up, so a burst slows
// the callers down instead of losing records. Remote sinks are fed from
// their own queues and cannot cause this wait.
func (e *Engine) Emit(evt Event) {
	if evt.ID == "" {
		evt.ID = ulid.New()
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	select {
	case e.ch <- evt:
		return
	default:
	}
	if e.logger != nil {
		e.logger.Warn("audit queue full, waiting for the audit log to catch up", "type", evt.Type, "id", evt.ID)
	}
	// Close takes the write lock, so it waits for this send; the loop keeps
	// draining e.ch until Close closes it, so the send always completes.
	e.ch <- evt
}

func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	close(e.ch)
	e.mu.Unlock()
	e.wg.Wait()

	// The loop has handed every event to the remote queues; let the workers
	// deliver what they can, then give up on the rest.
	for _, w := range e.queued {
		close(w.ch)
	}
	done := make(chan struct{})
	go func() {
		e.qwg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(e.drainTimeout):
		e.cancel()
		<-done
	}
	e.cancel()

	for _, w := range e.all {
		_ = w.sink.Close()
	}
}

func (e *Engine) loop() {
	defer e.wg.Done()
	for evt := range e.ch {
		for _, w := range e.sync {
			if err := w.sink.Write(context.Background(), evt); err != nil && e.logger != nil {
				e.logger.Error("audit sink write failed", "error", err, "type", evt.Type, "sink", w.kind, "output", w.output)
			}
		}
		for _, w := range e.queued {
			select {
			case w.ch <- evt:
			default:
				n := w.dropped.Add(1)
				// Log the first drop and then every 1000th, not every one.
				if e.logger != nil && (n == 1 || n%1000 == 0) {
					e.logger.Warn("audit sink queue full, dropping event for this sink",
						"sink", w.kind, "output", w.output, "dropped_total", n, "type", evt.Type)
				}
			}
		}
		// After the synchronous sinks, so readers of the audit log see the
		// event.
		if fn := e.onWrite.Load(); fn != nil && *fn != nil {
			(*fn)()
		}
	}
}

// runQueued delivers one remote sink's queue. After the drain timeout at
// Close, the remaining events are counted as dropped instead of sent.
func (e *Engine) runQueued(w *sinkWorker) {
	defer e.qwg.Done()
	for evt := range w.ch {
		if e.ctx.Err() != nil {
			w.dropped.Add(1)
			continue
		}
		if err := w.sink.Write(e.ctx, evt); err != nil {
			if e.ctx.Err() != nil {
				// Cut off by the drain timeout at Close: never delivered.
				w.dropped.Add(1)
				continue
			}
			if e.logger != nil {
				e.logger.Error("audit sink write failed", "error", err, "type", evt.Type, "sink", w.kind, "output", w.output)
			}
		}
	}
}

// Stats reports every configured sink, in configuration order.
func (e *Engine) Stats() []SinkStats {
	if e == nil {
		return nil
	}
	out := make([]SinkStats, 0, len(e.all))
	for _, w := range e.all {
		st := SinkStats{Output: w.output, Type: w.kind, Queued: w.ch != nil, Dropped: w.dropped.Load()}
		if w.ch != nil {
			st.QueueLength = len(w.ch)
		}
		out = append(out, st)
	}
	return out
}

// SetOnWrite registers a callback run after each event reaches the
// synchronous sinks.
func (e *Engine) SetOnWrite(fn func()) {
	e.onWrite.Store(&fn)
}
