package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

// notifyTarget is a single resolved notification sink: where to POST, in what
// format, and which event types to deliver.
type notifyTarget struct {
	typ    string
	url    string
	events map[EventType]bool // nil/empty => all event types
}

// wants reports whether this target should receive the given event type.
func (t notifyTarget) wants(et EventType) bool {
	// deploy.log is a high-volume live-tail stream meant for SSE/UI/metrics, not
	// chat sinks. It is opt-in only: never matched by the "empty = all" default,
	// so a target without an explicit events filter is not flooded with log lines.
	if et == EventDeployLog {
		return t.events[et]
	}
	if len(t.events) == 0 {
		return true
	}
	return t.events[et]
}

// Notifier subscribes to the event bus and forwards matching events to outbound
// webhooks (Slack, Discord, or a generic JSON endpoint). It is best-effort and
// never blocks the deploy path: sends are bounded-concurrent and time-limited,
// and a slow target only causes the bus to drop this subscriber's events (never
// the publisher's). Delivery failures are logged, not retried — the ledger and
// /events SSE stream remain the durable record.
type Notifier struct {
	targets []notifyTarget
	client  *http.Client
	// sem bounds in-flight HTTP sends so a burst can't spawn unbounded
	// goroutines; a full sem applies backpressure to the bus (which then drops).
	sem chan struct{}
}

// NewNotifier builds a Notifier from config targets. It returns nil when no
// targets are configured, so callers can hold an optional *Notifier and call
// Run unconditionally (Run is nil-safe). An unknown target type is skipped with
// a warning rather than failing startup — config validation already rejects it,
// this is just defense in depth.
func NewNotifier(targets []config.NotificationTarget) *Notifier {
	var resolved []notifyTarget
	for _, t := range targets {
		switch t.Type {
		case config.NotifySlack, config.NotifyDiscord, config.NotifyWebhook:
		default:
			slog.Warn("notifications: skipping target with unknown type", "type", t.Type)
			continue
		}
		var events map[EventType]bool
		if len(t.Events) > 0 {
			events = make(map[EventType]bool, len(t.Events))
			for _, e := range t.Events {
				events[EventType(e)] = true
			}
		}
		resolved = append(resolved, notifyTarget{typ: t.Type, url: t.URL, events: events})
	}
	if len(resolved) == 0 {
		return nil
	}
	return &Notifier{
		targets: resolved,
		client:  &http.Client{Timeout: 10 * time.Second},
		sem:     make(chan struct{}, 8),
	}
}

// Run subscribes to the bus and forwards events until ctx is cancelled. It
// folds in the replay backlog first so events buffered before the notifier
// attached are still delivered. Safe to call on a nil *Notifier (no-op).
func (n *Notifier) Run(ctx context.Context, bus *EventBus) {
	if n == nil {
		return
	}
	sub, backlog := bus.Subscribe()
	defer sub.Close()
	for _, ev := range backlog {
		n.dispatch(ctx, ev)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-sub.C:
			if !open {
				return
			}
			n.dispatch(ctx, ev)
		}
	}
}

// dispatch sends ev to every target that wants it. Each send runs in its own
// goroutine bounded by n.sem; acquiring the slot here (not in the goroutine)
// means a saturated set of targets backpressures the Run loop rather than
// spawning unbounded goroutines.
func (n *Notifier) dispatch(ctx context.Context, ev Event) {
	for _, t := range n.targets {
		if !t.wants(ev.Type) {
			continue
		}
		select {
		case n.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(t notifyTarget) {
			defer func() { <-n.sem }()
			if err := n.send(ctx, t, ev); err != nil {
				slog.Warn("notification delivery failed", "type", t.typ, "event", ev.Type, "err", err)
			}
		}(t)
	}
}

// send POSTs a single event to a single target in the target's payload format.
func (n *Notifier) send(ctx context.Context, t notifyTarget, ev Event) error {
	body, contentType, err := notifyPayload(t.typ, ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned %s", t.url, resp.Status)
	}
	return nil
}

// notifyPayload renders the request body + Content-Type for a target type.
// slack/discord get a chat message; webhook gets the raw event JSON. Exposed
// shape kept pure (no I/O) so it is unit-testable.
func notifyPayload(typ string, ev Event) (body []byte, contentType string, err error) {
	switch typ {
	case config.NotifySlack:
		b, err := json.Marshal(map[string]string{"text": notifyText(ev)})
		return b, "application/json", err
	case config.NotifyDiscord:
		b, err := json.Marshal(map[string]string{"content": notifyText(ev)})
		return b, "application/json", err
	case config.NotifyWebhook:
		b, err := json.Marshal(ev)
		return b, "application/json", err
	default:
		return nil, "", fmt.Errorf("unknown notification type %q", typ)
	}
}

// notifyText renders a one-line human-readable summary of an event for chat
// sinks: an emoji cue, the event type, then the non-empty identifying fields.
func notifyText(ev Event) string {
	var b strings.Builder
	if e := notifyEmoji(ev.Type); e != "" {
		b.WriteString(e)
		b.WriteByte(' ')
	}
	b.WriteString("shuttle ")
	b.WriteString(string(ev.Type))
	writeField(&b, "service", ev.Service)
	writeField(&b, "host", ev.Host)
	writeField(&b, "sha", shortSHA(ev.SHA))
	writeField(&b, "status", ev.Status)
	writeField(&b, "msg", ev.Message)
	return b.String()
}

func writeField(b *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	b.WriteString(" ")
	b.WriteString(key)
	b.WriteString("=")
	b.WriteString(val)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func notifyEmoji(et EventType) string {
	switch et {
	case EventDeploySucceeded:
		return "✅"
	case EventDeployFailed:
		return "❌"
	case EventDeployRolledBack, EventRollbackQueued:
		return "⏪"
	case EventDriftDetected:
		return "⚠️"
	case EventServiceRemoved, EventVolumesPurged:
		return "🗑️"
	case EventDeployQueued:
		return "🚀"
	default:
		return "ℹ️"
	}
}
