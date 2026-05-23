package orchestrator

import (
	"context"
	"testing"

	"github.com/neikow/shuttle/internal/token"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// fakeStream is a minimal grpc.ServerStream carrying a context.
type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeStream) Context() context.Context { return s.ctx }

func ctxWithToken(tok string) context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + tok})
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestTokenStreamInterceptor(t *testing.T) {
	store, err := openTestLedger(t)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const tok = "valid-token"
	if err := store.CreateAgentToken(ctx, "id1", "web1", token.Hash(tok)); err != nil {
		t.Fatal(err)
	}

	interceptor := TokenStreamInterceptor(store)
	info := &grpc.StreamServerInfo{FullMethod: "/shuttle.v1.AgentService/Register"}

	t.Run("valid token passes and injects host", func(t *testing.T) {
		var gotHost string
		err := interceptor(nil, &fakeStream{ctx: ctxWithToken(tok)}, info,
			func(_ any, ss grpc.ServerStream) error {
				gotHost, _ = tokenHostFromContext(ss.Context())
				return nil
			})
		if err != nil {
			t.Fatalf("want nil err, got %v", err)
		}
		if gotHost != "web1" {
			t.Errorf("host in context = %q, want web1", gotHost)
		}
	})

	t.Run("invalid token rejected", func(t *testing.T) {
		err := interceptor(nil, &fakeStream{ctx: ctxWithToken("nope")}, info,
			func(any, grpc.ServerStream) error { return nil })
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("missing token rejected", func(t *testing.T) {
		err := interceptor(nil, &fakeStream{ctx: context.Background()}, info,
			func(any, grpc.ServerStream) error { return nil })
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("want Unauthenticated, got %v", err)
		}
	})

	t.Run("revoked token rejected", func(t *testing.T) {
		if err := store.RevokeAgentToken(ctx, "id1"); err != nil {
			t.Fatal(err)
		}
		err := interceptor(nil, &fakeStream{ctx: ctxWithToken(tok)}, info,
			func(any, grpc.ServerStream) error { return nil })
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("want Unauthenticated for revoked, got %v", err)
		}
	})
}
