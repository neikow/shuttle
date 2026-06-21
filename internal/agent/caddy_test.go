package agent

import "testing"

func TestCaddyOptions_withDefaults(t *testing.T) {
	// Empty options fall back to the standard ports.
	got := CaddyOptions{}.withDefaults()
	if got.HTTPPort != defaultCaddyHTTPPort || got.HTTPSPort != defaultCaddyHTTPSPort {
		t.Errorf("default ports = %d/%d, want %d/%d", got.HTTPPort, got.HTTPSPort, defaultCaddyHTTPPort, defaultCaddyHTTPSPort)
	}
	if got.Image != "caddy:2-alpine" || got.Network != "shuttle" || got.Container != "shuttle-caddy" {
		t.Errorf("unexpected defaults: %+v", got)
	}

	// Explicit ports are preserved.
	got = CaddyOptions{HTTPPort: 8080, HTTPSPort: 8443}.withDefaults()
	if got.HTTPPort != 8080 || got.HTTPSPort != 8443 {
		t.Errorf("explicit ports = %d/%d, want 8080/8443", got.HTTPPort, got.HTTPSPort)
	}
}
