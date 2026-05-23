package infisical

import (
	"fmt"
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
	if p.Event != EventSecretsModified {
		t.Errorf("event = %q", p.Event)
	}
	if p.Env() != "prod" || p.Path() != "/services/api" {
		t.Errorf("env/path = %q %q, want prod /services/api", p.Env(), p.Path())
	}
}

func TestParse_rotationFailed(t *testing.T) {
	body := `{"event":"secrets.rotation-failed","project":{"environment":"prod","secretPath":"/services/api","rotationName":"db-creds","errorMessage":"timeout"}}`
	r := httptest.NewRequest("POST", "/webhook/infisical", strings.NewReader(body))

	p, err := NewHandler("").Parse(r)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Event != EventSecretsRotationFailed {
		t.Errorf("event = %q", p.Event)
	}
	if p.Project.RotationName != "db-creds" || p.Project.ErrorMessage != "timeout" {
		t.Errorf("rotation fields = %q %q", p.Project.RotationName, p.Project.ErrorMessage)
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

func TestParse_numericTimestamp(t *testing.T) {
	// Infisical sends timestamp as a JSON number (epoch ms); must not fail decode.
	body := `{"event":"test","project":{"environment":"dev","secretPath":"/"},"timestamp":1779570210642}`
	r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	p, err := NewHandler("").Parse(r)
	if err != nil {
		t.Fatalf("numeric timestamp rejected: %v", err)
	}
	if p.Event != "test" || p.Env() != "dev" {
		t.Errorf("event=%q env=%q, want test/dev", p.Event, p.Env())
	}
}

func TestParse_unsignedTestPing(t *testing.T) {
	// Infisical test pings arrive without a signature; must be accepted.
	r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"event":"test"}`))
	p, err := NewHandler("whsec").Parse(r)
	if err != nil {
		t.Fatalf("unsigned test ping rejected: %v", err)
	}
	if p.Event != "test" {
		t.Errorf("event = %q, want test", p.Event)
	}
}

func TestParse_signedTestPing(t *testing.T) {
	// A signed test ping must still verify correctly.
	secret := "whsec"
	body := `{"event":"test"}`
	sig := ComputeHeader("1700000000", []byte(body), secret)
	r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	r.Header.Set(SignatureHeader, sig)
	if _, err := NewHandler(secret).Parse(r); err != nil {
		t.Fatalf("signed test ping rejected: %v", err)
	}
}

func TestParse_badSignatureTestPing(t *testing.T) {
	// A present-but-wrong signature on a test ping must still be rejected.
	body := `{"event":"test"}`
	r := httptest.NewRequest("POST", "/x", strings.NewReader(body))
	r.Header.Set(SignatureHeader, "t=1,v1=deadbeef")
	if _, err := NewHandler("whsec").Parse(r); err == nil {
		t.Fatal("want signature error for bad-signed test ping")
	}
}

func TestVerifySignature_separators(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "s"
	// ComputeHeader emits "t=<ts>;<hex>"; also accept "v1=<hex>,t=<ts>" form.
	good := ComputeHeader("123", body, secret)
	parts := strings.SplitN(good, ";", 2) // ["t=123", "<hex>"]
	alt := "v1=" + parts[1] + "," + parts[0]
	if err := VerifySignature(body, secret, alt); err != nil {
		t.Errorf("v1= prefix + comma separator should verify: %v", err)
	}
}

func TestVerifySignature_bareHex(t *testing.T) {
	// Infisical sends "t=<ts>;<hex>" with no "v1=" prefix on the signature.
	body := []byte(`{"event":"secrets.modified"}`)
	secret := "s"
	ts := "1779568809550"
	mac := computeMAC(body, secret)
	header := fmt.Sprintf("t=%s;%x", ts, mac)
	if err := VerifySignature(body, secret, header); err != nil {
		t.Errorf("bare hex format should verify: %v", err)
	}
}
