package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/util/ulid"
)

type Sink interface {
	Write(context.Context, Event) error
	Close() error
}

type Engine struct {
	logger *slog.Logger
	ch     chan Event
	sinks  []Sink
	wg     sync.WaitGroup
	mu     sync.RWMutex
	closed bool
}

func NewEngine(logger *slog.Logger, sinks ...Sink) *Engine {
	e := &Engine{
		logger: logger,
		ch:     make(chan Event, 1024),
		sinks:  sinks,
	}
	e.wg.Add(1)
	go e.loop()
	return e
}

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
	default:
		if e.logger != nil {
			e.logger.Warn("audit queue full, dropping event", "type", evt.Type, "id", evt.ID)
		}
	}
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
	for _, sink := range e.sinks {
		_ = sink.Close()
	}
}

func (e *Engine) loop() {
	defer e.wg.Done()
	for evt := range e.ch {
		for _, sink := range e.sinks {
			if err := sink.Write(context.Background(), evt); err != nil && e.logger != nil {
				e.logger.Error("audit sink write failed", "error", err, "type", evt.Type)
			}
		}
	}
}
