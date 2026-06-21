package main

import "testing"

func TestDefaultCaddyImage(t *testing.T) {
	tests := map[string]string{
		"dev":    caddyImageRepo + ":latest",
		"":       caddyImageRepo + ":latest",
		"v1.2.3": caddyImageRepo + ":v1.2.3",
		"v0.9.0": caddyImageRepo + ":v0.9.0",
	}
	for version, want := range tests {
		if got := defaultCaddyImage(version); got != want {
			t.Errorf("defaultCaddyImage(%q) = %q, want %q", version, got, want)
		}
	}
}
