package mtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// issueCert creates a cert (self-signed if parent is nil) and writes cert.pem +
// key.pem into dir under the given prefix. Returns the parsed cert + key for
// use as a signing parent.
func issueCert(t *testing.T, dir, prefix string, san []string, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: prefix},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     san,
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent, parentKey
	} else {
		tmpl.IsCA = true
		tmpl.BasicConstraintsValid = true
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(filepath.Join(dir, prefix+".crt"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, prefix+".key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(der)
	return parsed, key
}

func TestMutualHandshake(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := issueCert(t, dir, "ca", nil, nil, nil)
	issueCert(t, dir, "orchestrator", []string{"orchestrator"}, ca, caKey)
	issueCert(t, dir, "agent", nil, ca, caKey)

	p := func(n string) string { return filepath.Join(dir, n) }
	serverCreds, err := ServerCreds(p("orchestrator.crt"), p("orchestrator.key"), p("ca.crt"))
	if err != nil {
		t.Fatalf("ServerCreds: %v", err)
	}
	clientCreds, err := ClientCreds(p("agent.crt"), p("agent.key"), p("ca.crt"), "orchestrator")
	if err != nil {
		t.Fatalf("ClientCreds: %v", err)
	}

	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := serverCreds.ServerHandshake(c1)
		errCh <- err
	}()
	if _, _, err := clientCreds.ClientHandshake(context.Background(), "orchestrator", c2); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

func TestServerCreds_missingFile(t *testing.T) {
	if _, err := ServerCreds("/nope/x.crt", "/nope/x.key", "/nope/ca.crt"); err == nil {
		t.Fatal("expected error for missing files")
	}
}

func TestClientCreds_badCA(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := issueCert(t, dir, "ca", nil, nil, nil)
	issueCert(t, dir, "agent", nil, ca, caKey)
	bad := filepath.Join(dir, "empty.crt")
	if err := os.WriteFile(bad, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := func(n string) string { return filepath.Join(dir, n) }
	if _, err := ClientCreds(p("agent.crt"), p("agent.key"), bad, "orchestrator"); err == nil {
		t.Fatal("expected error for CA with no certs")
	}
}

func TestServerTLSHandshake(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := issueCert(t, dir, "ca", nil, nil, nil)
	issueCert(t, dir, "orchestrator", []string{"orchestrator"}, ca, caKey)

	p := func(n string) string { return filepath.Join(dir, n) }
	serverCreds, err := ServerTLSCreds(p("orchestrator.crt"), p("orchestrator.key"))
	if err != nil {
		t.Fatalf("ServerTLSCreds: %v", err)
	}
	clientCreds, err := ClientTLSCreds(p("ca.crt"), "orchestrator")
	if err != nil {
		t.Fatalf("ClientTLSCreds: %v", err)
	}

	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := serverCreds.ServerHandshake(c1)
		errCh <- err
	}()
	if _, _, err := clientCreds.ClientHandshake(context.Background(), "orchestrator", c2); err != nil {
		t.Fatalf("client handshake (server-auth only): %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

func TestServerTLSCreds_missingFile(t *testing.T) {
	if _, err := ServerTLSCreds("/nope/x.crt", "/nope/x.key"); err == nil {
		t.Fatal("expected error for missing files")
	}
}

func TestClientTLSCreds_badCA(t *testing.T) {
	if _, err := ClientTLSCreds("/nope/ca.crt", "orchestrator"); err == nil {
		t.Fatal("expected error for missing CA")
	}
}
