package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/config"
	"github.com/neikow/shuttle/internal/secrets"
)

// writeTestCert writes a self-signed cert PEM with the given validity window and
// returns its path.
func writeTestCert(t *testing.T, dir string, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "shuttle-test"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func findCheck(checks []doctorCheck, name string) (doctorCheck, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func TestInspectCertFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	opts := doctorOpts{now: now, certWarnDays: 30}

	t.Run("valid", func(t *testing.T) {
		path := writeTestCert(t, t.TempDir(), now.AddDate(0, 0, -1), now.AddDate(1, 0, 0))
		if got := inspectCertFile(path, opts); got.Status != statusOK {
			t.Errorf("status = %v, want OK (%s)", got.Status, got.Detail)
		}
	})
	t.Run("expiring soon warns", func(t *testing.T) {
		path := writeTestCert(t, t.TempDir(), now.AddDate(0, 0, -1), now.AddDate(0, 0, 10))
		if got := inspectCertFile(path, opts); got.Status != statusWarn {
			t.Errorf("status = %v, want WARN (%s)", got.Status, got.Detail)
		}
	})
	t.Run("expired fails", func(t *testing.T) {
		path := writeTestCert(t, t.TempDir(), now.AddDate(0, 0, -10), now.AddDate(0, 0, -1))
		if got := inspectCertFile(path, opts); got.Status != statusFail {
			t.Errorf("status = %v, want FAIL (%s)", got.Status, got.Detail)
		}
	})
	t.Run("not yet valid fails", func(t *testing.T) {
		path := writeTestCert(t, t.TempDir(), now.AddDate(0, 0, 5), now.AddDate(1, 0, 0))
		if got := inspectCertFile(path, opts); got.Status != statusFail {
			t.Errorf("status = %v, want FAIL (%s)", got.Status, got.Detail)
		}
	})
	t.Run("missing file fails", func(t *testing.T) {
		if got := inspectCertFile(filepath.Join(dir, "nope.pem"), opts); got.Status != statusFail {
			t.Errorf("status = %v, want FAIL", got.Status)
		}
	})
	t.Run("garbage fails", func(t *testing.T) {
		path := filepath.Join(dir, "junk.pem")
		if err := os.WriteFile(path, []byte("not a pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := inspectCertFile(path, opts); got.Status != statusFail {
			t.Errorf("status = %v, want FAIL", got.Status)
		}
	})
}

// happyOpts returns probes that all succeed, for the all-clear baseline.
func happyOpts(now time.Time) doctorOpts {
	return doctorOpts{
		now:          now,
		certWarnDays: 30,
		lookPath:     func(string) (string, error) { return "/usr/bin/x", nil },
		gitLsRemote:  func(context.Context, string) error { return nil },
		dockerInfo:   func(context.Context) error { return nil },
		newProvider:  func(string) (secrets.Provider, error) { return nil, nil },
	}
}

func TestBuildDoctorReport_allClear(t *testing.T) {
	now := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	cert := writeTestCert(t, dir, now.AddDate(0, 0, -1), now.AddDate(1, 0, 0))
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.OrchestratorConfig{
		DataDir:        dir,
		RepoURL:        "file:///tmp/repo",
		GRPCTLSCert:    cert,
		GRPCTLSKey:     keyPath,
		AgentTokenAuth: true,
	}
	checks := buildDoctorReport(context.Background(), cfg, happyOpts(now))

	if c, ok := findCheck(checks, "grpc transport"); !ok || c.Status != statusOK {
		t.Errorf("grpc transport = %+v", c)
	}
	if c, ok := findCheck(checks, "tls cert"); !ok || c.Status != statusOK {
		t.Errorf("tls cert = %+v", c)
	}
	if c, ok := findCheck(checks, "repo reachable"); !ok || c.Status != statusOK {
		t.Errorf("repo reachable = %+v", c)
	}
	if countStatus(checks, statusFail) != 0 {
		t.Errorf("expected no failures, got %d: %+v", countStatus(checks, statusFail), checks)
	}
}

func TestBuildDoctorReport_gitMissingFails(t *testing.T) {
	now := time.Now()
	opts := happyOpts(now)
	opts.lookPath = func(name string) (string, error) {
		if name == "git" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	cfg := &config.OrchestratorConfig{DataDir: t.TempDir()}
	checks := buildDoctorReport(context.Background(), cfg, opts)
	if c, ok := findCheck(checks, "git binary"); !ok || c.Status != statusFail {
		t.Errorf("git binary = %+v, want FAIL", c)
	}
}

func TestBuildDoctorReport_dockerUnreachableWarns(t *testing.T) {
	opts := happyOpts(time.Now())
	opts.dockerInfo = func(context.Context) error { return errors.New("cannot connect") }
	cfg := &config.OrchestratorConfig{DataDir: t.TempDir()}
	checks := buildDoctorReport(context.Background(), cfg, opts)
	if c, ok := findCheck(checks, "docker"); !ok || c.Status != statusWarn {
		t.Errorf("docker = %+v, want WARN", c)
	}
}

func TestBuildDoctorReport_insecureTransportWarns(t *testing.T) {
	cfg := &config.OrchestratorConfig{DataDir: t.TempDir()} // no TLS, no token auth
	checks := buildDoctorReport(context.Background(), cfg, happyOpts(time.Now()))
	c, ok := findCheck(checks, "grpc transport")
	if !ok || c.Status != statusWarn {
		t.Errorf("grpc transport = %+v, want WARN", c)
	}
}

func TestBuildDoctorReport_secretsProviderError(t *testing.T) {
	opts := happyOpts(time.Now())
	opts.newProvider = func(string) (secrets.Provider, error) { return nil, errors.New("missing env") }
	cfg := &config.OrchestratorConfig{DataDir: t.TempDir(), SecretsProvider: "infisical"}
	checks := buildDoctorReport(context.Background(), cfg, opts)
	if c, ok := findCheck(checks, "secrets provider"); !ok || c.Status != statusFail {
		t.Errorf("secrets provider = %+v, want FAIL", c)
	}
}

func TestBuildDoctorReport_skipFlags(t *testing.T) {
	opts := happyOpts(time.Now())
	opts.skipGit = true
	opts.skipDocker = true
	cfg := &config.OrchestratorConfig{DataDir: t.TempDir()}
	checks := buildDoctorReport(context.Background(), cfg, opts)
	if _, ok := findCheck(checks, "git binary"); ok {
		t.Error("git check should be skipped")
	}
	if c, ok := findCheck(checks, "docker"); !ok || c.Detail != "skipped" {
		t.Errorf("docker = %+v, want skipped", c)
	}
}
