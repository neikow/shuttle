package orchestrator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
)

func TestNotifier_DeliversEvents(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer srv.Close()

	// slack target exercises the emoji/text formatting path.
	n := NewNotifier([]config.NotificationTarget{{Type: "slack", URL: srv.URL}})
	if n == nil {
		t.Fatal("NewNotifier returned nil for a configured target")
	}
	bus := NewEventBus()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx, bus)

	for _, et := range []EventType{EventDeploySucceeded, EventDeployFailed, EventDriftDetected, EventServiceRemoved} {
		bus.Publish(Event{Type: et, Service: "web", SHA: "0123456789abcdef"})
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(bodies)
		mu.Unlock()
		if got >= 4 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("notifier delivered %d events, want >=4", len(bodies))
}

func TestNewNotifier_NilWhenNoTargets(t *testing.T) {
	if NewNotifier(nil) != nil {
		t.Error("no targets -> nil notifier")
	}
}
