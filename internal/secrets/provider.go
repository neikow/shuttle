package secrets

import (
	"context"
	"fmt"
)

// Provider resolves secret keys to plaintext values. scope selects a named
// secret set — for Infisical it is the environment slug (a service's env_from);
// the empty scope means the provider's default environment.
type Provider interface {
	// Get returns the plaintext value for key in scope. Returns ErrNotFound if absent.
	Get(ctx context.Context, scope, key string) (string, error)
	// GetAll returns all secrets in scope as a map.
	GetAll(ctx context.Context, scope string) (map[string]string, error)
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
