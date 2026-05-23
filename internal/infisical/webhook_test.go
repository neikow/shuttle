package infisical

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParse_signedRoundTrip(t *testing.T) {
	secret := "whsec"
	body := `{"event":"secrets.modified","project":{"environment":"prod","secretPath":"/services/api"}}`
	sig := ComputeHeader("1700000000", []byte(body), secret)

	r := httptest.NewRequest("POST", "/webhook/infisical", strings.NewReader(body))
	r.Header.Set(SignatureHeader, sig)

	p, err := NewHandler(secret).Parse(r)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Event != "secrets.modified" {
		t.Errorf("event = %q", p.Event)
	}
	if p.Env() != "prod" || p.Path() != "/services/api" {
		t.Errorf("env/path = %q %q, want prod /services/api", p.Env(), p.Path())
	}
}

func TestParse_topLevelFields(t *testing.T) {
	body := `{"event":"secrets.modified","environment":"staging","secretPath":"/shared"}`
	r := httptest.NewRequest("POST", "/webhook/infisical", strings.NewReader(body))

	p, err := NewHandler("").Parse(r) // no secret => signature skipped
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Env() != "staging" || p.Path() != "/shared" {
		t.Errorf("env/path = %q %q", p.Env(), p.Path())
	}
}

func TestParse_defaultPathRoot(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"event":"e"}`))
	p, err := NewHandler("").Parse(r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path() != "/" {
		t.Errorf("default path = %q, want /", p.Path())
	}
}

func TestParse_badSignature(t *testing.T) {
	body := `{"event":"e"}`
	r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	r.Header.Set(SignatureHeader, "t=1,v1=deadbeef")

	if _, err := NewHandler("whsec").Parse(r); err == nil {
		t.Fatal("want signature error")
	}
}

func TestParse_missingSignature(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{}`))
	if _, err := NewHandler("whsec").Parse(r); err == nil {
		t.Fatal("want error for missing signature header")
	}
}

func TestVerifySignature_separators(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "s"
	// Semicolon separator and reversed field order must still verify.
	good := ComputeHeader("123", body, secret)
	parts := strings.Split(strings.TrimPrefix(good, ""), ",")
	alt := parts[1] + ";" + parts[0] // "v1=...;t=123"
	if err := VerifySignature(body, secret, alt); err != nil {
		t.Errorf("reordered/semicolon header should verify: %v", err)
	}
}
