package mtls

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPinnedHTTPClientMatch(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	pin := SPKIPin(ts.Certificate())
	if !strings.HasPrefix(pin, PinPrefix) {
		t.Fatalf("pin %q missing prefix", pin)
	}

	client, err := PinnedHTTPClient(pin, 5*time.Second)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("matching pin should connect: %v", err)
	}
	_ = resp.Body.Close()
}

func TestPinnedHTTPClientMismatch(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()

	client, err := PinnedHTTPClient("sha256:wrongwrongwrongwrongwrongwrongwrongwrong0=", 5*time.Second)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := client.Get(ts.URL); err == nil {
		t.Fatal("mismatched pin must fail the connection")
	}
}

func TestPinnedHTTPClientEmptyPinUsesSystemRoots(t *testing.T) {
	// An empty pin yields a normal client; against httptest's untrusted cert the
	// default verification must reject it (proving we did not skip verification).
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()

	client, err := PinnedHTTPClient("", 5*time.Second)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if _, err := client.Get(ts.URL); err == nil {
		t.Fatal("empty pin must still verify against system roots (untrusted cert should fail)")
	}
}
