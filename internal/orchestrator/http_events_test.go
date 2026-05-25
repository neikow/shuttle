package orchestrator

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readSSEEvent reads lines until the next `data:` frame and decodes it, failing
// the test if nothing arrives within a short window.
func readSSEEvent(t *testing.T, r *bufio.Reader) Event {
	t.Helper()
	type result struct {
		ev  Event
		err error
	}
	ch := make(chan result, 1)
	go func() {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			data, ok := strings.CutPrefix(strings.TrimRight(line, "\n"), "data: ")
			if !ok {
				continue
			}
			var ev Event
			err = json.Unmarshal([]byte(data), &ev)
			ch <- result{ev: ev, err: err}
			return
		}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read sse: %v", res.err)
		}
		return res.ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out reading sse event")
		return Event{}
	}
}

func TestHandleEvents_Unauthorized(t *testing.T) {
	s := NewHTTPServer("tok", nil, nil)
	s.SetEventBus(NewEventBus())
	srv := httptest.NewServer(s)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestHandleEvents_StreamsBacklogThenLive(t *testing.T) {
	bus := NewEventBus()
	bus.Publish(Event{Type: EventDeployQueued, Service: "backlog"})

	s := NewHTTPServer("tok", nil, nil)
	s.SetEventBus(bus)
	srv := httptest.NewServer(s)
	defer srv.Close()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/events", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("want text/event-stream, got %q", ct)
	}

	r := bufio.NewReader(resp.Body)
	// Backlog (published before connect) arrives first.
	if ev := readSSEEvent(t, r); ev.Service != "backlog" {
		t.Fatalf("want backlog event, got %+v", ev)
	}
	// A live event published after the client is attached is streamed through.
	bus.Publish(Event{Type: EventDeploySucceeded, Service: "live", Host: "web-1"})
	got := readSSEEvent(t, r)
	if got.Service != "live" || got.Type != EventDeploySucceeded || got.Host != "web-1" {
		t.Fatalf("unexpected live event: %+v", got)
	}
}
