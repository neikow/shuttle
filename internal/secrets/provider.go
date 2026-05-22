package secrets

import "context"

// Provider resolves secret keys to plaintext values.
type Provider interface {
	// Get returns the plaintext value for key. Returns ErrNotFound if absent.
	Get(ctx context.Context, key string) (string, error)
	// GetAll returns all secrets as a map.
	GetAll(ctx context.Context) (map[string]string, error)
}

// ErrNotFound is returned when a secret key is absent.
type ErrNotFound struct {
	Key string
}

func (e ErrNotFound) Error() string {
	return "secret not found: " + e.Key
}
