package secrets

import (
	"context"
	"fmt"
)

// Provider resolves secret keys to plaintext values.
type Provider interface {
	// Get returns the plaintext value for key. Returns ErrNotFound if absent.
	Get(ctx context.Context, key string) (string, error)
	// GetAll returns all secrets as a map.
	GetAll(ctx context.Context) (map[string]string, error)
}

// NewProvider constructs a Provider by name. "" and "none" return (nil, nil),
// meaning secrets are not resolved (env passthrough off). "infisical" reads
// credentials from the environment (see InfisicalProvider).
func NewProvider(name string) (Provider, error) {
	switch name {
	case "", "none":
		return nil, nil
	case "infisical":
		return NewInfisical()
	default:
		return nil, fmt.Errorf("unknown secrets provider %q", name)
	}
}

// ErrNotFound is returned when a secret key is absent.
type ErrNotFound struct {
	Key string
}

func (e ErrNotFound) Error() string {
	return "secret not found: " + e.Key
}
