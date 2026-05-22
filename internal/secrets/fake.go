package secrets

import (
	"context"
	"sync"
)

// Fake is an in-memory Provider for tests.
type Fake struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewFake(initial map[string]string) *Fake {
	m := make(map[string]string, len(initial))
	for k, v := range initial {
		m[k] = v
	}
	return &Fake{data: m}
}

func (f *Fake) Set(key, value string) {
	f.mu.Lock()
	f.data[key] = value
	f.mu.Unlock()
}

func (f *Fake) Get(_ context.Context, key string) (string, error) {
	f.mu.RLock()
	v, ok := f.data[key]
	f.mu.RUnlock()
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}

func (f *Fake) GetAll(_ context.Context) (map[string]string, error) {
	f.mu.RLock()
	out := make(map[string]string, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	f.mu.RUnlock()
	return out, nil
}
