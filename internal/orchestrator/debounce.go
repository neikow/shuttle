package orchestrator

import (
	"sync"
	"time"
)

// SecretChange identifies a changed Infisical secret folder in an environment.
type SecretChange struct {
	Env  string
	Path string
}

// changeDebouncer coalesces bursts of secret-change events into a single delayed
// callback. Several Infisical changes often land in quick succession (e.g.
// editing multiple values), so debouncing collapses them into one redeploy pass
// after a quiet window. Changes are unioned across triggers within the window.
type changeDebouncer struct {
	window time.Duration
	fire   func(changes []SecretChange)

	mu      sync.Mutex
	pending map[SecretChange]struct{}
	timer   *time.Timer
}

func newChangeDebouncer(window time.Duration, fire func(changes []SecretChange)) *changeDebouncer {
	return &changeDebouncer{
		window:  window,
		fire:    fire,
		pending: make(map[SecretChange]struct{}),
	}
}

// Trigger records a change and (re)arms the timer. A zero window fires
// synchronously with just this change, mainly for tests.
func (d *changeDebouncer) Trigger(c SecretChange) {
	if d.window <= 0 {
		d.fire([]SecretChange{c})
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending[c] = struct{}{}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = time.AfterFunc(d.window, d.flush)
}

func (d *changeDebouncer) flush() {
	d.mu.Lock()
	changes := make([]SecretChange, 0, len(d.pending))
	for c := range d.pending {
		changes = append(changes, c)
	}
	d.pending = make(map[SecretChange]struct{})
	d.timer = nil
	d.mu.Unlock()
	d.fire(changes)
}
