package mtls

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// PinPrefix labels a certificate pin string. The value following it is the
// base64 SHA-256 of a certificate's SubjectPublicKeyInfo.
const PinPrefix = "sha256:"

// SPKIPin returns the trust-on-first-use pin for a certificate: PinPrefix plus
// the base64 SHA-256 of its SubjectPublicKeyInfo (DER). Pinning the public-key
// info rather than the whole certificate lets the orchestrator renew its cert
// without invalidating outstanding join commands, provided the key is reused.
func SPKIPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return PinPrefix + base64.StdEncoding.EncodeToString(sum[:])
}

// PinnedHTTPClient returns an http.Client that trusts a TLS server iff its leaf
// certificate matches pin (trust-on-first-use). The pin is conveyed out-of-band
// by `shuttle enroll`, which computes it from the orchestrator's live cert over
// the operator's already-trusted channel. When pin is empty the client verifies
// normally against the system roots. timeout bounds the whole request.
func PinnedHTTPClient(pin string, timeout time.Duration) (*http.Client, error) {
	if pin == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Verification is done by VerifyConnection against the pin, so the
		// default chain build is skipped — a self-signed orchestrator cert is
		// accepted exactly when its key matches the pin.
		InsecureSkipVerify: true, //nolint:gosec // the pin check below is the trust anchor
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("no peer certificate presented")
			}
			got := SPKIPin(cs.PeerCertificates[0])
			if subtle.ConstantTimeCompare([]byte(got), []byte(pin)) != 1 {
				return fmt.Errorf("certificate pin mismatch: server presented %s, expected %s", got, pin)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}
