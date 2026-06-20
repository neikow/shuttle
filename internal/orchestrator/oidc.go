package orchestrator

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/neikow/shuttle/internal/config"
)

// OIDCAuthenticator verifies OpenID Connect bearer tokens (JWTs) presented on
// the HTTP control plane and maps their claims to a control-plane Role. It is
// the third identity source in HTTPServer.resolveRole, after the static
// bootstrap bearer and the named control tokens — reusing the same
// read<deploy<admin model so OIDC callers flow through the existing requireRole
// enforcement and become the audit actor by their token identity.
//
// JWT signature verification and JWKS key rotation are delegated to
// github.com/coreos/go-oidc — the canonical Go OIDC verifier — rather than
// hand-rolled, consistent with the project's "accept a dependency where
// correctness is hard to get right" stance (cf. cosign, prometheus histograms).
type OIDCAuthenticator struct {
	verifier      *oidc.IDTokenVerifier
	rolesClaim    string
	usernameClaim string
	mapping       map[string]Role

	// Public, non-secret parameters the web UI needs to run the browser login
	// flow (Authorization Code + PKCE). Served at GET /auth/config.
	issuer   string
	audience string
	scopes   string
}

// OIDCPublicConfig is the non-secret OIDC information the web UI needs to start a
// browser login: the issuer to discover, the client_id (audience) to request a
// token for, and the scopes to ask for.
type OIDCPublicConfig struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	Scopes   string `json:"scopes"`
}

// PublicConfig returns the non-secret parameters the SPA needs to log in.
func (a *OIDCAuthenticator) PublicConfig() OIDCPublicConfig {
	return OIDCPublicConfig{Issuer: a.issuer, ClientID: a.audience, Scopes: a.scopes}
}

// NewOIDCAuthenticator performs OIDC discovery against cfg.Issuer (a network
// call — the issuer must be reachable at startup) and builds a verifier bound to
// cfg.Audience. It validates the role mapping up front so a typo'd role fails
// fast at boot rather than silently denying every user.
func NewOIDCAuthenticator(ctx context.Context, cfg config.OIDCConfig) (*OIDCAuthenticator, error) {
	if cfg.Issuer == "" {
		return nil, fmt.Errorf("oidc: issuer is required")
	}
	if cfg.Audience == "" {
		return nil, fmt.Errorf("oidc: audience is required")
	}
	mapping := make(map[string]Role, len(cfg.RoleMapping))
	for group, roleStr := range cfg.RoleMapping {
		role, err := ParseRole(roleStr)
		if err != nil {
			return nil, fmt.Errorf("oidc: role_mapping[%q]: %w", group, err)
		}
		mapping[group] = role
	}
	if len(mapping) == 0 {
		return nil, fmt.Errorf("oidc: role_mapping must not be empty")
	}

	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery against %s: %w", cfg.Issuer, err)
	}

	rolesClaim := cfg.RolesClaim
	if rolesClaim == "" {
		rolesClaim = "groups"
	}
	usernameClaim := cfg.UsernameClaim
	if usernameClaim == "" {
		usernameClaim = "sub"
	}
	scopes := cfg.Scopes
	if scopes == "" {
		scopes = "openid profile email"
	}
	return &OIDCAuthenticator{
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.Audience}),
		rolesClaim:    rolesClaim,
		usernameClaim: usernameClaim,
		mapping:       mapping,
		issuer:        cfg.Issuer,
		audience:      cfg.Audience,
		scopes:        scopes,
	}, nil
}

// verify checks the raw JWT and returns the caller's role and identity name. ok
// is true when the token is validly signed and issued for this audience —
// regardless of whether it maps to any role. A validly-authenticated token with
// no mapped role returns an empty Role (rank 0), so requireRole answers 403
// (authenticated but unauthorized) rather than 401, mirroring how a too-low
// control token is treated.
func (a *OIDCAuthenticator) verify(ctx context.Context, raw string) (Role, string, bool) {
	idToken, err := a.verifier.Verify(ctx, raw)
	if err != nil {
		return "", "", false
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return "", "", false
	}
	name := stringClaim(claims, a.usernameClaim)
	if name == "" {
		name = idToken.Subject
	}
	return a.highestRole(claims), name, true
}

// highestRole reads the configured roles claim (a string or list of strings)
// and returns the highest-ranked role any of its values map to.
func (a *OIDCAuthenticator) highestRole(claims map[string]any) Role {
	var best Role
	for _, v := range claimStrings(claims[a.rolesClaim]) {
		if role, ok := a.mapping[v]; ok && roleRank(role) > roleRank(best) {
			best = role
		}
	}
	return best
}

// stringClaim returns the named claim if it is a string, else "".
func stringClaim(claims map[string]any, key string) string {
	if s, ok := claims[key].(string); ok {
		return s
	}
	return ""
}

// claimStrings normalizes a claim value that may be a single string or a list
// (of strings, or of arbitrary values from which strings are kept) into a slice
// of strings. IdPs emit a "groups"/"roles" claim either way.
func claimStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
