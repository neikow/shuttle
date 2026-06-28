package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// certSANs collects the Subject Alternative Names the generated orchestrator
// cert must carry so agents can verify it: the SAN they're told to expect
// (advertise_server_name), the local loopback names, and the hostnames parsed
// from the advertised gRPC and control URLs. IP literals become IP SANs, names
// become DNS SANs.
func certSANs(serverName, advertiseAddr, controlURL string) []string {
	sans := []string{serverName, "localhost", "127.0.0.1"}
	if h := hostOnly(advertiseAddr); h != "" {
		sans = append(sans, h)
	}
	if u, err := url.Parse(controlURL); err == nil && u.Hostname() != "" {
		sans = append(sans, u.Hostname())
	}
	return sans
}

// hostOnly strips a :port from a host:port, tolerating a bare host.
func hostOnly(addr string) string {
	if addr == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// ensureSelfSignedCert writes a fresh self-signed EC (P-256) cert/key pair to
// certPath/keyPath unless both already exist. The cert is its own root (IsCA),
// which is exactly what the token-enrollment handoff expects: the orchestrator
// hands this cert back to a redeeming agent as the trust anchor, so no separate
// CA is needed. Returns whether a new cert was created.
func ensureSelfSignedCert(certPath, keyPath string, sans []string) (bool, error) {
	_, certErr := os.Stat(certPath)
	_, keyErr := os.Stat(keyPath)
	if certErr == nil && keyErr == nil {
		return false, nil // both present — never clobber a real cert
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return false, fmt.Errorf("generate serial: %w", err)
	}

	cn := "orchestrator"
	if len(sans) > 0 && sans[0] != "" {
		cn = sans[0]
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, s := range sans {
		if s == "" {
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, s)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return false, fmt.Errorf("create certificate: %w", err)
	}

	if dir := filepath.Dir(certPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}
	if dir := filepath.Dir(keyPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return false, fmt.Errorf("marshal key: %w", err)
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}
