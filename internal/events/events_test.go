package events

import "testing"

func TestBrokerCoalescesAndFilters(t *testing.T) {
	b := NewBroker()
	sessions := b.Subscribe(TopicSessions)
	both := b.Subscribe(TopicSessions | TopicAudit)
	defer sessions.Close()

	b.Publish(TopicAudit)
	select {
	case <-sessions.C:
		t.Fatal("sessions subscriber woken by audit")
	default:
	}
	<-both.C

	// A burst coalesces into one pending wake-up and never blocks.
	for i := 0; i < 100; i++ {
		b.Publish(TopicSessions)
	}
	<-sessions.C
	select {
	case <-sessions.C:
		t.Fatal("burst not coalesced")
	default:
	}

	<-both.C // drain the burst's pending wake-up
	both.Close()
	b.Publish(TopicSessions)
	select {
	case <-both.C:
		t.Fatal("closed subscription still notified")
	default:
	}

	var nilBroker *Broker
	nilBroker.Publish(TopicAudit)
	nilBroker.Subscribe(TopicAudit).Close()
	nilBroker.Publisher(TopicAudit)()
}
