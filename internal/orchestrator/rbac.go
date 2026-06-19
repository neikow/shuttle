package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/neikow/shuttle/internal/token"
)

// Role is a control-plane access tier. Roles are totally ordered by rank
// (read < deploy < admin); a token of a given role may call any endpoint whose
// minimum role is at or below its own.
type Role string

const (
	RoleRead   Role = "read"   // read-only: list/inspect endpoints
	RoleDeploy Role = "deploy" // read + trigger deploy/rollback/prune
	RoleAdmin  Role = "admin"  // deploy + enrollment, webhook + token management
)

// roleRank maps a role to its tier; 0 is an unknown/invalid role (denies all).
func roleRank(r Role) int {
	switch r {
	case RoleRead:
		return 1
	case RoleDeploy:
		return 2
	case RoleAdmin:
		return 3
	default:
		return 0
	}
}

// ParseRole validates a role string.
func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleRead, RoleDeploy, RoleAdmin:
		return Role(s), nil
	default:
		return "", fmt.Errorf("invalid role %q (want read, deploy, or admin)", s)
	}
}

// identity is the resolved caller of an authenticated request: the token's name
// (empty for the static bootstrap bearer) and its role.
type identity struct {
	Name string
	Role Role
}

type identityCtxKey struct{}

func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

func identityFrom(ctx context.Context) (identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(identity)
	return id, ok
}

// bearerFromRequest extracts the bearer token from the Authorization header.
func bearerFromRequest(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return auth[len("Bearer "):]
}

// SetOIDC attaches an OIDC authenticator so per-user OIDC bearer tokens are
// accepted as a third identity source. Call before serving; nil leaves OIDC off.
func (s *HTTPServer) SetOIDC(a *OIDCAuthenticator) { s.oidc = a }

// resolveRole maps a presented bearer token to a role and name, trying three
// identity sources in order: (1) the static config bearer_token — the bootstrap
// admin (no name); (2) a named, role-scoped control token in the ledger; (3)
// when configured, an OIDC JWT (identity = its subject/username claim). ok
// reports that the caller was authenticated — it may still carry an empty Role
// (rank 0), which requireRole answers with 403 rather than 401. ok is false only
// for a token that matches none of the three sources.
func (s *HTTPServer) resolveRole(ctx context.Context, tok string) (Role, string, bool) {
	if tok == "" {
		return "", "", false
	}
	if s.token != "" && constantTimeEqual(tok, s.token) {
		return RoleAdmin, "", true
	}
	// Named control token (an opaque random string), looked up in the ledger. A
	// hit is authoritative; a miss or lookup error falls through to OIDC so an
	// OIDC JWT (which is not in control_tokens) still gets its chance.
	if s.ledger != nil {
		name, roleStr, found, err := s.ledger.LookupControlToken(ctx, token.Hash(tok))
		switch {
		case err != nil:
			slog.Error("control token lookup failed", "err", err)
		case found:
			role, perr := ParseRole(roleStr)
			if perr != nil {
				// A token with a corrupt role grants nothing.
				slog.Error("control token has invalid role", "name", name, "role", roleStr)
				return "", "", false
			}
			return role, name, true
		}
	}
	// OIDC bearer (a JWT). Only attempted on JWT-shaped tokens, so an opaque
	// control/static token never incurs a signature verify.
	if s.oidc != nil && looksLikeJWT(tok) {
		if role, name, ok := s.oidc.verify(ctx, tok); ok {
			return role, name, true
		}
	}
	return "", "", false
}

// looksLikeJWT reports whether tok has the compact JWS shape
// (header.payload.signature) — three non-empty dot-separated segments. Used to
// skip OIDC verification of opaque (non-JWT) bearer tokens.
func looksLikeJWT(tok string) bool {
	parts := strings.Split(tok, ".")
	return len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != ""
}

// requireRole wraps a handler so it only runs for a caller whose token resolves
// to at least min. 401 for a missing/invalid token, 403 for a valid token with
// insufficient role. On success the resolved identity is stashed in the request
// context so the audit log can record the token's name as the actor.
func (s *HTTPServer) requireRole(min Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role, name, ok := s.resolveRole(r.Context(), bearerFromRequest(r))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if roleRank(role) < roleRank(min) {
			http.Error(w, "forbidden: requires role "+string(min), http.StatusForbidden)
			return
		}
		next(w, r.WithContext(withIdentity(r.Context(), identity{Name: name, Role: role})))
	}
}
