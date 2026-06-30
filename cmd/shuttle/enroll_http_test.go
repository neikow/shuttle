package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"v":1}`))
		case "/bad":
			http.Error(w, "nope", http.StatusForbidden)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	body, _, err := doJSON(ctx, srv.Client(), http.MethodGet, srv.URL+"/ok", "tok", nil)
	if err != nil || string(body) != `{"v":1}` {
		t.Fatalf("doJSON ok: body=%q err=%v", body, err)
	}
	if _, _, err := doJSON(ctx, srv.Client(), http.MethodGet, srv.URL+"/bad", "tok", nil); err == nil {
		t.Error("non-2xx should error")
	}
	if _, _, err := doJSON(ctx, srv.Client(), http.MethodGet, "http://127.0.0.1:0/x", "tok", nil); err == nil {
		t.Error("connection failure should error")
	}
}

func TestListHostsAndEnroll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/hosts":
			_, _ = w.Write([]byte(`[{"name":"web1","labels":{"role":"edge"}},{"name":"db1"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/enroll":
			_, _ = w.Write([]byte(`{"id":"t1","host":"web1","join_token":"JT","expires_at_unix_ms":123}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	hosts, err := listHosts(ctx, srv.Client(), srv.URL, "tok")
	if err != nil || len(hosts) != 2 || hosts[0].Name != "web1" {
		t.Fatalf("listHosts = %+v err=%v", hosts, err)
	}

	res, _, err := enrollHostReq(ctx, srv.Client(), srv.URL, "tok", "web1")
	if err != nil || res.JoinToken != "JT" || res.Host != "web1" {
		t.Fatalf("enrollHostReq = %+v err=%v", res, err)
	}
}
