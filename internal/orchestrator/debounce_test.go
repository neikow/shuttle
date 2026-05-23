package orchestrator

import (
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestChangeDebouncer_coalesces(t *testing.T) {
	var (
		mu    sync.Mutex
		fires [][]SecretChange
	)
	done := make(chan struct{})
	d := newChangeDebouncer(40*time.Millisecond, func(c []SecretChange) {
		mu.Lock()
		fires = append(fires, c)
		mu.Unlock()
		close(done)
	})

	// Three rapid changes (one duplicate) should collapse into a single fire.
	d.Trigger(SecretChange{Env: "prod", Path: "/a"})
	d.Trigger(SecretChange{Env: "prod", Path: "/b"})
	d.Trigger(SecretChange{Env: "prod", Path: "/a"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("debouncer never fired")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fires) != 1 {
		t.Fatalf("want 1 fire, got %d", len(fires))
	}
	got := fires[0]
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []SecretChange{{Env: "prod", Path: "/a"}, {Env: "prod", Path: "/b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("coalesced changes = %v, want %v", got, want)
	}
}

func TestChangeDebouncer_zeroWindowSync(t *testing.T) {
	var got []SecretChange
	d := newChangeDebouncer(0, func(c []SecretChange) { got = c })
	d.Trigger(SecretChange{Env: "prod", Path: "/x"})
	if len(got) != 1 || got[0].Path != "/x" {
		t.Fatalf("zero window should fire synchronously, got %v", got)
	}
}
