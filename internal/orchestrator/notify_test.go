package orchestrator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

func TestNotifyPayload(t *testing.T) {
	ev := Event{Type: EventDeployFailed, Service: "web", Host: "web-1", SHA: "abcdef1234567890", Status: "FAILED"}

	tests := []struct {
		typ         string
		wantCT      string
		wantSubstr  []string // substrings that must appear in the body
		isRawJSON   bool     // webhook posts the full event
		jsonHasText bool     // slack uses {"text":...}
	}{
		{typ: config.NotifySlack, wantCT: "application/json", jsonHasText: true, wantSubstr: []string{"deploy.failed", "web", "abcdef1"}},
		{typ: config.NotifyDiscord, wantCT: "application/json", wantSubstr: []string{"deploy.failed", "web-1"}},
		{typ: config.NotifyWebhook, wantCT: "application/json", isRawJSON: true, wantSubstr: []string{"deploy.failed", "FAILED"}},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			body, ct, err := notifyPayload(tt.typ, ev)
			if err != nil {
				t.Fatalf("notifyPayload: %v", err)
			}
			if ct != tt.wantCT {
				t.Errorf("content-type = %q, want %q", ct, tt.wantCT)
			}
			for _, s := range tt.wantSubstr {
				if !strings.Contains(string(body), s) {
					t.Errorf("body %q missing %q", body, s)
				}
			}
			if tt.jsonHasText {
				var m map[string]string
				if err := json.Unmarshal(body, &m); err != nil || m["text"] == "" {
					t.Errorf("slack body not {\"text\":...}: %q (%v)", body, err)
				}
			}
			if tt.isRawJSON {
				var got Event
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("webhook body not an Event: %v", err)
				}
				if got.Type != ev.Type || got.Service != ev.Service {
					t.Errorf("webhook event = %+v, want type/service of %+v", got, ev)
				}
			}
		})
	}

	if _, _, err := notifyPayload("bogus", ev); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestNotifyTextShortensSHAAndSkipsEmpty(t *testing.T) {
	got := notifyText(Event{Type: EventDeploySucceeded, Service: "api", SHA: "0123456789abcdef"})
	if !strings.Contains(got, "service=api") {
		t.Errorf("missing service field: %q", got)
	}
	if !strings.Contains(got, "sha=0123456") || strings.Contains(got, "0123456789") {
		t.Errorf("sha not shortened to 7: %q", got)
	}
	if strings.Contains(got, "host=") || strings.Contains(got, "status=") {
		t.Errorf("empty fields should be omitted: %q", got)
	}
}

func TestNotifyTargetWants(t *testing.T) {
	all := notifyTarget{}
	if !all.wants(EventDeployFailed) {
		t.Error("empty filter should accept all")
	}
	filtered := notifyTarget{events: map[EventType]bool{EventDeployFailed: true}}
	if !filtered.wants(EventDeployFailed) {
		t.Error("filter should accept listed type")
	}
	if filtered.wants(EventDeploySucceeded) {
		t.Error("filter should reject unlisted type")
	}

	// deploy.log is opt-in: the empty "all" filter must NOT match it (it would
	// flood chat sinks), but an explicit subscription does.
	if all.wants(EventDeployLog) {
		t.Error("empty filter should not match deploy.log")
	}
	optedIn := notifyTarget{events: map[EventType]bool{EventDeployLog: true}}
	if !optedIn.wants(EventDeployLog) {
		t.Error("explicit deploy.log subscription should match")
	}
}

func TestNewNotifierNilWhenEmpty(t *testing.T) {
	if NewNotifier(nil) != nil {
		t.Error("no targets should yield nil notifier")
	}
	if NewNotifier([]config.NotificationTarget{{Type: "bogus", URL: "x"}}) != nil {
		t.Error("only-invalid targets should yield nil notifier")
	}
}

func TestNotifierRunDeliversMatchingEvent(t *testing.T) {
	received := make(chan Event, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var ev Event
		_ = json.Unmarshal(b, &ev)
		received <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNotifier([]config.NotificationTarget{
		{Type: config.NotifyWebhook, URL: srv.URL, Events: []string{string(EventDeployFailed)}},
	})
	if n == nil {
		t.Fatal("notifier should be non-nil")
	}

	bus := NewEventBus()
	go n.Run(t.Context(), bus)

	// Filtered-out event must not be delivered; matching one must be.
	bus.Publish(Event{Type: EventDeploySucceeded, Service: "skip"})
	bus.Publish(Event{Type: EventDeployFailed, Service: "web"})

	select {
	case got := <-received:
		if got.Type != EventDeployFailed || got.Service != "web" {
			t.Fatalf("got %+v, want deploy.failed/web", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for notification")
	}

	// The skipped event should not arrive after the matching one.
	select {
	case extra := <-received:
		t.Fatalf("unexpected extra delivery: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}
