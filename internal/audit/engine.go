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
	cancel context.CancelFunc
}

func NewEngine(logger *slog.Logger, sinks ...Sink) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		logger: logger,
		ch:     make(chan Event, 1024),
		sinks:  sinks,
		cancel: cancel,
	}
	e.wg.Add(1)
	go e.loop(ctx)
	return e
}

func (e *Engine) Emit(evt Event) {
	if evt.ID == "" {
		evt.ID = ulid.New()
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
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
	e.cancel()
	close(e.ch)
	e.wg.Wait()
	for _, sink := range e.sinks {
		_ = sink.Close()
	}
}

func (e *Engine) loop(ctx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-e.ch:
			if !ok {
				return
			}
			for _, sink := range e.sinks {
				if err := sink.Write(ctx, evt); err != nil && e.logger != nil {
					e.logger.Error("audit sink write failed", "error", err, "type", evt.Type)
				}
			}
		}
	}
}
