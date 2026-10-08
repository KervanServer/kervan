// Package events is a tiny in-process change notifier. Producers publish a
// topic when state changes; subscribers receive a coalesced wake-up and then
// read the current state themselves, so slow consumers never block
// producers and bursts collapse into a single notification.
package events

import "sync"

type Topic uint8

const (
	TopicSessions Topic = 1 << iota
	TopicTransfers
	TopicAudit
)

// Broker fans notifications out to subscribers. A nil *Broker is inert.
type Broker struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[*Subscription]struct{})}
}

// Subscription receives a signal on C after any subscribed topic changes.
type Subscription struct {
	C      <-chan struct{}
	ch     chan struct{}
	topics Topic
	broker *Broker
}

// Subscribe registers interest in topics (bitwise OR of Topic values).
func (b *Broker) Subscribe(topics Topic) *Subscription {
	ch := make(chan struct{}, 1)
	sub := &Subscription{C: ch, ch: ch, topics: topics, broker: b}
	if b == nil {
		return sub
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

// Close unregisters the subscription.
func (s *Subscription) Close() {
	if s == nil || s.broker == nil {
		return
	}
	s.broker.mu.Lock()
	delete(s.broker.subs, s)
	s.broker.mu.Unlock()
}

// Publish signals every subscriber of topic without blocking.
func (b *Broker) Publish(topic Topic) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs {
		if sub.topics&topic == 0 {
			continue
		}
		select {
		case sub.ch <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
}

// Publisher returns a func that publishes topic, for wiring into components
// that take a plain change callback.
func (b *Broker) Publisher(topic Topic) func() {
	return func() { b.Publish(topic) }
}
