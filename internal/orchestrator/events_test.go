package orchestrator

import (
	"sync"
	"testing"
	"time"
)

// recv reads one event from ch or fails after a short timeout.
func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestEventBus_PublishDelivers(t *testing.T) {
	b := NewEventBus()
	sub, backlog := b.Subscribe()
	defer sub.Close()
	if len(backlog) != 0 {
		t.Fatalf("expected empty backlog, got %d", len(backlog))
	}

	b.Publish(Event{Type: EventDeployQueued, Service: "web"})
	got := recv(t, sub.C)
	if got.Type != EventDeployQueued || got.Service != "web" {
		t.Fatalf("unexpected event: %+v", got)
	}
	if got.Time.IsZero() {
		t.Fatal("Publish should default Time")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	b := NewEventBus()
	s1, _ := b.Subscribe()
	defer s1.Close()
	s2, _ := b.Subscribe()
	defer s2.Close()

	b.Publish(Event{Type: EventDeploySucceeded, Service: "api"})
	for i, ch := range []<-chan Event{s1.C, s2.C} {
		if got := recv(t, ch); got.Service != "api" {
			t.Fatalf("subscriber %d: got %+v", i, got)
		}
	}
}

func TestEventBus_CloseStopsDelivery(t *testing.T) {
	b := NewEventBus()
	sub, _ := b.Subscribe()
	sub.Close()

	// Channel is closed: a receive returns the zero value with ok=false.
	if _, ok := <-sub.C; ok {
		t.Fatal("expected closed channel after Close")
	}
	// Publishing to a bus with no live subscribers must not panic.
	b.Publish(Event{Type: EventDeployQueued})
	// Close is idempotent.
	sub.Close()
}

func TestEventBus_DropsWhenSubscriberFull(t *testing.T) {
	b := NewEventBus()
	b.subBuffer = 1 // tiny buffer so the second publish overflows
	sub, _ := b.Subscribe()
	defer sub.Close()

	// Never read from sub.C. First event fills the buffer; the rest drop.
	b.Publish(Event{Type: EventDeployQueued})
	b.Publish(Event{Type: EventDeployQueued})
	b.Publish(Event{Type: EventDeployQueued})

	if got := b.Dropped(); got != 2 {
		t.Fatalf("expected 2 dropped, got %d", got)
	}
}

func TestEventBus_ReplayBacklog(t *testing.T) {
	b := NewEventBus()
	b.Publish(Event{Type: EventDeployQueued, Service: "a"})
	b.Publish(Event{Type: EventDeploySucceeded, Service: "b"})

	sub, backlog := b.Subscribe()
	defer sub.Close()
	if len(backlog) != 2 || backlog[0].Service != "a" || backlog[1].Service != "b" {
		t.Fatalf("unexpected backlog (want a,b chronological): %+v", backlog)
	}

	// New event arrives on the channel only — not duplicated into the backlog.
	b.Publish(Event{Type: EventDeployFailed, Service: "c"})
	if got := recv(t, sub.C); got.Service != "c" {
		t.Fatalf("expected live event c, got %+v", got)
	}
}

func TestEventBus_RingCapsAndOrders(t *testing.T) {
	b := NewEventBus()
	b.ring = make([]Event, 3) // cap replay at 3 to exercise wrap-around

	for _, svc := range []string{"1", "2", "3", "4", "5"} {
		b.Publish(Event{Type: EventDeployQueued, Service: svc})
	}
	recent := b.Recent()
	if len(recent) != 3 {
		t.Fatalf("expected 3 retained, got %d", len(recent))
	}
	for i, want := range []string{"3", "4", "5"} {
		if recent[i].Service != want {
			t.Fatalf("recent[%d]=%s, want %s (chronological)", i, recent[i].Service, want)
		}
	}
}

func TestEventBus_NilSafe(t *testing.T) {
	var b *EventBus
	b.Publish(Event{Type: EventDeployQueued}) // must not panic
	if got := b.Recent(); got != nil {
		t.Fatalf("nil bus Recent should be nil, got %v", got)
	}
	if got := b.Dropped(); got != 0 {
		t.Fatalf("nil bus Dropped should be 0, got %d", got)
	}
	sub, backlog := b.Subscribe()
	if sub == nil || sub.C == nil {
		t.Fatal("nil bus Subscribe should return a usable (empty) subscription")
	}
	if backlog != nil {
		t.Fatal("nil bus Subscribe backlog should be nil")
	}
	sub.Close() // must not panic
}

func TestEventBus_ConcurrentPublishSubscribe(t *testing.T) {
	b := NewEventBus()
	var wg sync.WaitGroup

	// Subscribers that connect, drain, and disconnect concurrently.
	for range 8 {
		wg.Go(func() {
			sub, _ := b.Subscribe()
			defer sub.Close()
			deadline := time.After(50 * time.Millisecond)
			for {
				select {
				case <-sub.C:
				case <-deadline:
					return
				}
			}
		})
	}

	// Publishers hammering the bus.
	for range 8 {
		wg.Go(func() {
			for range 100 {
				b.Publish(Event{Type: EventDeployQueued, Service: "x"})
			}
		})
	}

	wg.Wait() // -race flags any data race here
}
