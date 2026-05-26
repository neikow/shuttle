//go:build embedui

package orchestrator

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnableUI_servesBundleAndFallback(t *testing.T) {
	s := NewHTTPServer("tok", nil, nil)
	s.EnableUI()
	srv := httptest.NewServer(s)
	defer srv.Close()

	// Root serves the SPA shell.
	res, err := http.Get(srv.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if !strings.Contains(string(body), `id="root"`) {
		t.Fatalf("index.html shell not served")
	}

	// Unknown client route falls back to index.html (SPA).
	res2, err := http.Get(srv.URL + "/ui/deep/link")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(res2.Body)
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusOK || !strings.Contains(string(body2), `id="root"`) {
		t.Fatalf("SPA fallback failed: status=%d", res2.StatusCode)
	}

	// Bare /ui redirects to /ui/.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res3, err := client.Get(srv.URL + "/ui")
	if err != nil {
		t.Fatal(err)
	}
	_ = res3.Body.Close()
	if res3.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("bare /ui status = %d, want 301", res3.StatusCode)
	}
}
