package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestRenderServiceYAML(t *testing.T) {
	cases := []struct {
		name string
		in   serviceScaffold
		want []string
	}{
		{"compose", serviceScaffold{Name: "web", Host: "h1", Kind: "compose"}, []string{"name: web", "host: h1"}},
		{"docker+domain+port", serviceScaffold{Name: "web", Host: "h1", Kind: "docker", Domains: []string{"a.com"}, Port: 80}, []string{"domains:", "  - a.com", "port: 80"}},
		{"external", serviceScaffold{Name: "x", Host: "h1", Kind: "external", Upstream: "host.docker.internal:9", Domains: []string{"x.com"}}, []string{"external:", "  upstream: host.docker.internal:9"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderServiceYAML(c.in)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q\n---\n%s", w, got)
				}
			}
			if probs := config.ValidateBytes(config.FileKindService, []byte(got)); len(probs) > 0 {
				t.Errorf("generated invalid: %+v\n%s", probs, got)
			}
		})
	}
}

func TestRenderServiceYAML_errors(t *testing.T) {
	if _, err := renderServiceYAML(serviceScaffold{Name: "x", Host: "h", Kind: "external"}); err == nil {
		t.Error("external without upstream/domain should error")
	}
	if _, err := renderServiceYAML(serviceScaffold{Name: "x", Host: "h", Kind: "bogus"}); err == nil {
		t.Error("unknown kind should error")
	}
}

func TestScaffoldService_files(t *testing.T) {
	repo := t.TempDir()
	paths, err := scaffoldService(repo, serviceScaffold{Name: "web", Host: "h1", Kind: "docker", Image: "nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("want service + compose, got %v", paths)
	}
	if _, err := os.Stat(filepath.Join(repo, "services", "web", "web.yaml")); err != nil {
		t.Error("service file not created")
	}
	if _, err := os.Stat(filepath.Join(repo, "services", "web", "docker-compose.yml")); err != nil {
		t.Error("compose file not created")
	}
	// External: no compose file.
	paths, err = scaffoldService(repo, serviceScaffold{Name: "ext", Host: "h1", Kind: "external", Upstream: "u:1", Domains: []string{"e.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 {
		t.Errorf("external should create only the service file, got %v", paths)
	}
	// Refuse overwrite.
	if _, err := scaffoldService(repo, serviceScaffold{Name: "web", Host: "h1", Kind: "compose"}); err == nil {
		t.Error("should refuse to overwrite an existing service")
	}
}

func TestScaffoldMerge_hostsAndDNS_loadable(t *testing.T) {
	repo := t.TempDir()

	if _, err := scaffoldHost(repo, "web1", []string{"region=eu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffoldHost(repo, "web2", nil); err != nil {
		t.Fatal(err)
	}
	// Duplicate host rejected.
	if _, err := scaffoldHost(repo, "web1", nil); err == nil {
		t.Error("duplicate host should be rejected")
	}

	if _, err := scaffoldDNSProvider(repo, "ovh", "ovh", "ovh-eu"); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffoldCertificate(repo, "star", "ovh", []string{"*.example.com"}); err != nil {
		t.Fatal(err)
	}
	// A service tying it together (host + tls_certificate references).
	if _, err := scaffoldService(repo, serviceScaffold{Name: "web", Host: "web1", Kind: "docker", Image: "nginx", Domains: []string{"web.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// The whole scaffolded repo must load (cross-file refs resolve).
	if _, err := config.Load(repo); err != nil {
		t.Fatalf("scaffolded repo failed to load: %v", err)
	}
}

func TestMergeListEntry_preservesComments(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "hosts.yaml")
	seed := "# my hosts\nhosts:\n  - name: web1 # the edge box\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scaffoldHost(repo, "web2", nil); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"# my hosts", "# the edge box", "name: web1", "name: web2"} {
		if !strings.Contains(s, want) {
			t.Errorf("merged file lost %q\n---\n%s", want, s)
		}
	}
}
