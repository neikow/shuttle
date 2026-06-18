package secrets

import (
	"context"
	"fmt"
)

// Scope identifies a secret set. For Infisical, Env is the environment slug (a
// service's env_from) and Path is the folder. An empty field falls back to the
// provider's configured default for that axis.
type Scope struct {
	Env  string
	Path string
}

// Provider resolves secret keys to plaintext values within a Scope.
type Provider interface {
	// Get returns the plaintext value for key in scope. Returns ErrNotFound if absent.
	Get(ctx context.Context, scope Scope, key string) (string, error)
	// GetAll returns all secrets in scope as a map.
	GetAll(ctx context.Context, scope Scope) (map[string]string, error)
}

// NewProvider constructs a Provider by name. "" and "none" return (nil, nil),
// meaning secrets are not resolved (env passthrough off). "infisical" reads
// credentials from the environment (see InfisicalProvider); "file" reads dotenv
// files from SHUTTLE_SECRETS_DIR (see FileProvider).
func NewProvider(name string) (Provider, error) {
	switch name {
	case "", "none":
		return nil, nil
	case "infisical":
		return NewInfisical()
	case "file":
		return NewFileProvider()
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
