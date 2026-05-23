package secrets

import (
	"context"
	"fmt"
	"os"

	infisical "github.com/infisical/go-sdk"
)

// InfisicalProvider fetches secrets from Infisical using universal auth.
// Env vars: INFISICAL_CLIENT_ID, INFISICAL_CLIENT_SECRET, INFISICAL_PROJECT_ID, INFISICAL_ENV.
type InfisicalProvider struct {
	client      infisical.InfisicalClientInterface
	projectID   string
	environment string
	secretPath  string
}

func NewInfisical() (*InfisicalProvider, error) {
	clientID := os.Getenv("INFISICAL_CLIENT_ID")
	clientSecret := os.Getenv("INFISICAL_CLIENT_SECRET")
	projectID := os.Getenv("INFISICAL_PROJECT_ID")
	environment := os.Getenv("INFISICAL_ENV")
	if environment == "" {
		environment = "production"
	}
	secretPath := os.Getenv("INFISICAL_SECRET_PATH")
	if secretPath == "" {
		secretPath = "/"
	}

	if clientID == "" || clientSecret == "" || projectID == "" {
		return nil, fmt.Errorf("INFISICAL_CLIENT_ID, INFISICAL_CLIENT_SECRET, INFISICAL_PROJECT_ID required")
	}

	client := infisical.NewInfisicalClient(context.Background(), infisical.Config{
		SiteUrl: os.Getenv("INFISICAL_SITE_URL"),
	})
	_, err := client.Auth().UniversalAuthLogin(clientID, clientSecret)
	if err != nil {
		return nil, fmt.Errorf("infisical auth: %w", err)
	}

	return &InfisicalProvider{
		client:      client,
		projectID:   projectID,
		environment: environment,
		secretPath:  secretPath,
	}, nil
}

// envFor resolves the Infisical environment for a request: the scope's Env when
// set, otherwise the provider's default environment.
func (p *InfisicalProvider) envFor(scope Scope) string {
	if scope.Env != "" {
		return scope.Env
	}
	return p.environment
}

// pathFor resolves the Infisical folder for a request: the scope's Path when
// set, otherwise the provider's default path.
func (p *InfisicalProvider) pathFor(scope Scope) string {
	if scope.Path != "" {
		return scope.Path
	}
	return p.secretPath
}

func (p *InfisicalProvider) Get(_ context.Context, scope Scope, key string) (string, error) {
	secret, err := p.client.Secrets().Retrieve(infisical.RetrieveSecretOptions{
		SecretKey:   key,
		ProjectID:   p.projectID,
		Environment: p.envFor(scope),
		SecretPath:  p.pathFor(scope),
	})
	if err != nil {
		return "", fmt.Errorf("infisical get %q: %w", key, err)
	}
	return secret.SecretValue, nil
}

func (p *InfisicalProvider) GetAll(_ context.Context, scope Scope) (map[string]string, error) {
	res, err := p.client.Secrets().ListSecrets(infisical.ListSecretsOptions{
		ProjectID:   p.projectID,
		Environment: p.envFor(scope),
		SecretPath:  p.pathFor(scope),
	})
	if err != nil {
		return nil, fmt.Errorf("infisical list: %w", err)
	}
	out := make(map[string]string, len(res.Secrets))
	for _, s := range res.Secrets {
		out[s.SecretKey] = s.SecretValue
	}
	return out, nil
}
