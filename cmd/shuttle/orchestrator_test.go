package main

import (
	"context"
	"net"
	"testing"
	"time"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestStopGRPC_idleIsGraceful: with no active RPCs, GracefulStop completes well
// within the timeout and stopGRPC reports it did not have to force.
func TestStopGRPC_idleIsGraceful(t *testing.T) {
	srv := grpc.NewServer()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()

	if forced := stopGRPC(srv, 2*time.Second); forced {
		t.Error("idle server should stop gracefully, not be forced")
	}
}

// TestStopGRPC_forcesWithActiveStream reproduces the Ctrl+C hang: an open
// long-lived server stream keeps GracefulStop from returning, so stopGRPC must
// fall back to a forced Stop within the timeout instead of blocking forever.
func TestStopGRPC_forcesWithActiveStream(t *testing.T) {
	srv := grpc.NewServer()

	started := make(chan struct{})
	desc := &grpc.ServiceDesc{
		ServiceName: "test.Blocking",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Hold",
			ServerStreams: true,
			ClientStreams: true,
			Handler: func(_ any, stream grpc.ServerStream) error {
				close(started)
				<-stream.Context().Done() // hold the stream open until force-close
				return stream.Context().Err()
			},
		}},
	}
	srv.RegisterService(desc, struct{}{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	stream, err := conn.NewStream(context.Background(), &desc.Streams[0], "/test.Blocking/Hold")
	if err != nil {
		t.Fatal(err)
	}
	// Sending a message forces the RPC headers out so the server handler runs.
	if err := stream.SendMsg(&shuttlev1.Heartbeat{TsUnixMs: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("server stream handler never started")
	}

	deadline := make(chan bool, 1)
	go func() { deadline <- stopGRPC(srv, 300*time.Millisecond) }()
	select {
	case forced := <-deadline:
		if !forced {
			t.Error("expected forced stop with an active stream")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stopGRPC blocked past timeout — the Ctrl+C hang is not fixed")
	}
}
