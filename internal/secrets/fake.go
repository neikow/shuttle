package secrets

import (
	"context"
	"maps"
	"sync"
)

// Fake is an in-memory Provider for tests. The zero Scope is the default secret
// set; named scopes (an env/path pair) can be seeded with SetScope to exercise
// per-scope resolution.
type Fake struct {
	mu     sync.RWMutex
	data   map[string]string           // default scope (zero Scope)
	scopes map[Scope]map[string]string // per-scope overrides
}

func NewFake(initial map[string]string) *Fake {
	m := make(map[string]string, len(initial))
	maps.Copy(m, initial)
	return &Fake{data: m, scopes: make(map[Scope]map[string]string)}
}

func (f *Fake) Set(key, value string) {
	f.mu.Lock()
	f.data[key] = value
	f.mu.Unlock()
}

// SetScope seeds the secrets for a scope, overriding the default set for callers
// that pass that scope.
func (f *Fake) SetScope(scope Scope, m map[string]string) {
	cp := make(map[string]string, len(m))
	maps.Copy(cp, m)
	f.mu.Lock()
	f.scopes[scope] = cp
	f.mu.Unlock()
}

// scopeMap returns the secret set for scope (caller holds the lock). The zero
// scope is the default set; a named-but-unseeded scope resolves to nothing.
func (f *Fake) scopeMap(scope Scope) map[string]string {
	if scope == (Scope{}) {
		return f.data
	}
	return f.scopes[scope]
}

func (f *Fake) Get(_ context.Context, scope Scope, key string) (string, error) {
	f.mu.RLock()
	v, ok := f.scopeMap(scope)[key]
	f.mu.RUnlock()
	if !ok {
		return "", ErrNotFound{Key: key}
	}
	return v, nil
}

func (f *Fake) GetAll(_ context.Context, scope Scope) (map[string]string, error) {
	f.mu.RLock()
	src := f.scopeMap(scope)
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	f.mu.RUnlock()
	return out, nil
}
