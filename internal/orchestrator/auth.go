package orchestrator

import (
	"context"
	"strings"

	"github.com/neikow/shuttle/internal/ledger"
	"github.com/neikow/shuttle/internal/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type ctxKey int

const tokenHostKey ctxKey = iota

// authStream wraps a grpc.ServerStream to override its context, carrying the
// host resolved from the agent's enrollment token.
type authStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authStream) Context() context.Context { return s.ctx }

// tokenHostFromContext returns the host an authenticated agent's token is scoped
// to, when token auth is in effect.
func tokenHostFromContext(ctx context.Context) (string, bool) {
	h, ok := ctx.Value(tokenHostKey).(string)
	return h, ok
}

// TokenStreamInterceptor authenticates the agent stream with a bearer token from
// gRPC metadata, validated against the ledger. The token's host is stashed in
// the stream context for Register to cross-check.
func TokenStreamInterceptor(store *ledger.Store) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		tok := bearerFromContext(ss.Context())
		if tok == "" {
			return status.Error(codes.Unauthenticated, "missing agent token")
		}
		host, ok, err := store.AgentTokenHost(ss.Context(), token.Hash(tok))
		if err != nil {
			return status.Error(codes.Internal, "token lookup failed")
		}
		if !ok {
			return status.Error(codes.Unauthenticated, "invalid or revoked agent token")
		}
		ctx := context.WithValue(ss.Context(), tokenHostKey, host)
		return handler(srv, &authStream{ServerStream: ss, ctx: ctx})
	}
}

// bearerFromContext extracts a bearer token from the incoming "authorization"
// metadata header.
func bearerFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(vals[0], prefix) {
		return ""
	}
	return strings.TrimPrefix(vals[0], prefix)
}
