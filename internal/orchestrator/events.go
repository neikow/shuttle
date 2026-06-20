package orchestrator

import (
	"sync"
	"sync/atomic"
	"time"
)

// EventType identifies a published orchestrator event. Values are dotted
// "<subject>.<verb>" strings, stable enough to use as metric labels and in a
// notification stream.
type EventType string

const (
	EventDeployQueued     EventType = "deploy.queued"
	EventDeploySucceeded  EventType = "deploy.succeeded"
	EventDeployFailed     EventType = "deploy.failed"
	EventDeployRolledBack EventType = "deploy.rolled_back"
	EventRollbackQueued   EventType = "rollback.queued"
	EventDriftDetected    EventType = "drift.detected"
	EventServiceRemoved   EventType = "service.removed"
	EventVolumesPurged    EventType = "volumes.purged"
	EventBackupQueued     EventType = "backup.queued"
	EventBackupSucceeded  EventType = "backup.succeeded"
	EventBackupFailed     EventType = "backup.failed"
	EventRestoreSucceeded EventType = "restore.succeeded"
	EventRestoreFailed    EventType = "restore.failed"
)

// Event is a single thing that happened in the orchestrator. One flat struct
// (rather than a type per kind) so subscribers can fan it out and serialize it
// uniformly; per-event extras go in Detail.
type Event struct {
	Type     EventType         `json:"type"`
	Service  string            `json:"service,omitempty"`
	Host     string            `json:"host,omitempty"`
	DeployID string            `json:"deploy_id,omitempty"`
	SHA      string            `json:"sha,omitempty"`
	Status   string            `json:"status,omitempty"`
	Message  string            `json:"message,omitempty"`
	Time     time.Time         `json:"time"`
	Detail   map[string]string `json:"detail,omitempty"`
}

const (
	// defaultSubBuffer bounds each subscriber's channel. A subscriber that
	// can't keep up has events dropped (counted) rather than stalling the
	// publisher — a deploy must never block on a slow notification client.
	defaultSubBuffer = 64
	// defaultRingSize is how many recent events are retained for replay to
	// late subscribers (e.g. a websocket client that just connected).
	defaultRingSize = 256
)

// EventBus is an in-process publish/subscribe hub. Publishers (the deploy,
// reconcile, and teardown paths) call Publish; subscribers (a notification
// stream, a metrics collector) call Subscribe. Delivery is best-effort: a
// subscriber whose buffer is full misses events. The bus holds no durable
// state — the ledger remains the source of truth for deploy history.
//
// All methods are safe on a nil *EventBus (they no-op / return zero), so the
// bus can be optional: callers hold a possibly-nil *EventBus and publish
// unconditionally.
type EventBus struct {
	subBuffer int

	mu       sync.Mutex
	subs     map[int]chan Event
	nextID   int
	ring     []Event
	ringNext int
	ringFull bool

	dropped atomic.Uint64
}

// NewEventBus returns a bus with default buffer and replay sizes.
func NewEventBus() *EventBus {
	return &EventBus{
		subBuffer: defaultSubBuffer,
		subs:      make(map[int]chan Event),
		ring:      make([]Event, defaultRingSize),
	}
}

// Publish records the event in the replay ring and delivers it to every current
// subscriber without blocking. A subscriber with a full buffer is skipped and
// the drop is counted (see Dropped). Sends happen under the lock, but each is
// non-blocking, so Publish never waits on a consumer.
func (b *EventBus) Publish(ev Event) {
	if b == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.ring[b.ringNext] = ev
	b.ringNext = (b.ringNext + 1) % len(b.ring)
	if b.ringNext == 0 {
		b.ringFull = true
	}

	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			b.dropped.Add(1)
		}
	}
}

// Subscription is a live event feed. Read from C; call Close exactly once when
// done (Close is idempotent). Closing removes the subscription and closes C, so
// a `for ev := range sub.C` loop terminates.
type Subscription struct {
	C    <-chan Event
	bus  *EventBus
	id   int
	once sync.Once
}

// Subscribe registers a new subscriber and returns it together with the replay
// backlog (the recent events retained at subscribe time, oldest first). Events
// in the backlog are not also delivered on the channel, so there is no overlap.
func (b *EventBus) Subscribe() (*Subscription, []Event) {
	if b == nil {
		return &Subscription{C: make(chan Event)}, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, b.subBuffer)
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	return &Subscription{C: ch, bus: b, id: id}, b.recentLocked()
}

// Close detaches the subscription and closes its channel. Safe to call multiple
// times.
func (s *Subscription) Close() {
	s.once.Do(func() {
		if s.bus == nil {
			return
		}
		s.bus.mu.Lock()
		defer s.bus.mu.Unlock()
		if ch, ok := s.bus.subs[s.id]; ok {
			delete(s.bus.subs, s.id)
			close(ch)
		}
	})
}

// Recent returns the retained replay backlog, oldest first.
func (b *EventBus) Recent() []Event {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recentLocked()
}

// recentLocked returns the ring contents in chronological order. Caller holds mu.
func (b *EventBus) recentLocked() []Event {
	if !b.ringFull {
		if b.ringNext == 0 {
			return nil
		}
		out := make([]Event, b.ringNext)
		copy(out, b.ring[:b.ringNext])
		return out
	}
	out := make([]Event, len(b.ring))
	n := copy(out, b.ring[b.ringNext:])
	copy(out[n:], b.ring[:b.ringNext])
	return out
}

// Dropped returns the total number of events skipped because a subscriber's
// buffer was full. A growing value means a consumer can't keep up.
func (b *EventBus) Dropped() uint64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}
