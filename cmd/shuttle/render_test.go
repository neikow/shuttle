package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/neikow/shuttle/internal/orchestrator"
)

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:          "0B",
		512:        "512B",
		1024:       "1.0K",
		1536:       "1.5K",
		1048576:    "1.0M",
		1073741824: "1.0G",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("0123456789abcdef0000"); got != "0123456789ab" {
		t.Errorf("shortSHA long = %q", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA short = %q", got)
	}
}

func TestRenderCheck(t *testing.T) {
	report := &orchestrator.CheckReport{
		SHA:         "0123456789abcdef",
		HasProvider: true,
		GitCredentials: []orchestrator.GitCredentialCheckResult{
			{RepoPrefix: "github.com/me/iac", Key: "TOK"},
			{RepoPrefix: "github.com/me/bad", Key: "BAD", Err: "denied"},
		},
		DNSProviders: []orchestrator.DNSCredentialCheckResult{
			{Provider: "ovh", Type: "ovh"},
		},
		DNSRecords: []orchestrator.DNSRecordCheck{
			{FQDN: "app.example.com", Type: "A", Value: "1.2.3.4", Provider: "ovh"},
			{FQDN: "lab.home.test", Type: "A", Value: "100.64.0.5", Provider: "home", Manual: true},
			{Err: "host web1 has no public address"},
		},
		Services: []orchestrator.ServiceCheck{
			{Service: "ok", Env: "prod", BasePath: "/shared", ServicePath: "/services/ok", Schema: []string{"A"}},
			{Service: "noenv"},
			{Service: "lit", Schema: []string{"X"}},
			{Service: "miss", Env: "prod", MissingKeys: []string{"K1"}},
			{Service: "boom", Err: "load failed"},
			{Service: "warned", Schema: []string{"A"}, BasePath: "/s", ServicePath: "/svc/warned", Env: "prod", Warnings: []string{"rolling needs no fixed port"}},
		},
	}
	var buf bytes.Buffer
	renderCheck(&buf, report)
	out := buf.String()

	for _, want := range []string{
		"repo synced at 0123456789ab",
		"github.com/me/iac",
		"✗ github.com/me/bad",
		"ovh (ovh): credentials present",
		"✓ A app.example.com -> 1.2.3.4 (provider ovh)",
		"create it manually",
		"✗ host web1 has no public address",
		"✓ ok (env=prod",
		"✓ noenv: no env declared",
		"no provider lookups",
		"✗ miss (env=prod): unresolved 1 ref(s): K1",
		"✗ boom: load failed",
		"! warned: rolling needs no fixed port",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderCheck output missing %q\n---\n%s", want, out)
		}
	}
}

func TestDoctorGlyphAndRender(t *testing.T) {
	if statusOK.glyph() != "✓" || statusWarn.glyph() != "!" || statusFail.glyph() != "✗" {
		t.Errorf("glyphs = %q/%q/%q", statusOK.glyph(), statusWarn.glyph(), statusFail.glyph())
	}
	checks := []doctorCheck{
		{Name: "config", Status: statusOK, Detail: "parsed"},
		{Name: "docker", Status: statusWarn, Detail: "not reachable"},
		{Name: "tls", Status: statusFail, Detail: "expired"},
	}
	if n := countStatus(checks, statusFail); n != 1 {
		t.Errorf("countStatus fail = %d, want 1", n)
	}
	if n := countStatus(checks, statusOK); n != 1 {
		t.Errorf("countStatus ok = %d, want 1", n)
	}
	var buf bytes.Buffer
	renderDoctor(&buf, checks)
	out := buf.String()
	for _, want := range []string{"✓ config", "! docker", "✗ tls", "expired"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderDoctor missing %q in:\n%s", want, out)
		}
	}
}

func TestPrintEvent(t *testing.T) {
	var buf bytes.Buffer
	printEvent(&buf, orchestrator.Event{
		Type:     "deploy.succeeded",
		Service:  "web",
		DeployID: "d-1",
		SHA:      "0123456789abcdef0000",
		Status:   "success",
		Message:  "done",
		Time:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	out := buf.String()
	for _, want := range []string{"deploy.succeeded", "service=web", "deploy_id=d-1", "sha=0123456789ab", "status=success", "(done)"} {
		if !strings.Contains(out, want) {
			t.Errorf("printEvent missing %q in %q", want, out)
		}
	}
}
