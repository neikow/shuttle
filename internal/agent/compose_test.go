package agent

import (
	"reflect"
	"testing"
)

func TestNewDriver(t *testing.T) {
	tests := []struct {
		name      string
		dockerBin string
		wantBin   string
		wantErr   bool
	}{
		{name: "compose", wantBin: "docker"},
		{name: "", wantBin: "docker"},       // default
		{name: "docker", wantBin: "docker"}, // alias
		{name: "compose", dockerBin: "/custom/docker", wantBin: "/custom/docker"},
		{name: "synology", wantBin: "/usr/local/bin/docker"}, // DSM default path
		{name: "synology", dockerBin: "/opt/bin/docker", wantBin: "/opt/bin/docker"},
		{name: "bogus", wantErr: true},
	}
	for _, tt := range tests {
		d, err := NewDriver(tt.name, tt.dockerBin)
		if tt.wantErr {
			if err == nil {
				t.Errorf("NewDriver(%q,%q): expected error, got nil", tt.name, tt.dockerBin)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NewDriver(%q,%q): %v", tt.name, tt.dockerBin, err)
		}
		cd, ok := d.(*ComposeDriver)
		if !ok {
			t.Fatalf("NewDriver(%q): want *ComposeDriver, got %T", tt.name, d)
		}
		if cd.bin != tt.wantBin {
			t.Errorf("NewDriver(%q,%q): bin = %q, want %q", tt.name, tt.dockerBin, cd.bin, tt.wantBin)
		}
		if !reflect.DeepEqual(cd.compose, []string{"compose"}) {
			t.Errorf("NewDriver(%q): compose prefix = %v, want [compose]", tt.name, cd.compose)
		}
	}
}

func TestComposeArgs(t *testing.T) {
	d := NewComposeDriver()
	got := d.composeArgs("/w/docker-compose.yml", "/w/.env", "up", "-d")
	want := []string{"compose", "-f", "/w/docker-compose.yml", "--env-file", "/w/.env", "up", "-d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("composeArgs = %v, want %v", got, want)
	}
}
