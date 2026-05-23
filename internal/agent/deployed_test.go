package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedFromDisk(t *testing.T) {
	base := t.TempDir()
	// Two real compose workspaces, one bare dir (no compose), one stray file.
	for _, svc := range []string{"whoami", "api"} {
		dir := filepath.Join(base, svc)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(base, "notaservice"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "stray.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	s := newDeployedSet()
	if n := s.seedFromDisk(base); n != 2 {
		t.Fatalf("seeded %d, want 2", n)
	}
	snap := s.snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d entries, want 2: %+v", len(snap), snap)
	}
	got, ok := snap["whoami"]
	if !ok {
		t.Fatal("whoami not seeded")
	}
	if got.workDir != filepath.Join(base, "whoami") {
		t.Errorf("workDir = %q, want %q", got.workDir, filepath.Join(base, "whoami"))
	}
	if got.sha != "" {
		t.Errorf("sha = %q, want empty after restart", got.sha)
	}
	if _, ok := snap["notaservice"]; ok {
		t.Error("dir without docker-compose.yml should not be seeded")
	}
}

func TestSeedFromDisk_missingBaseDir(t *testing.T) {
	s := newDeployedSet()
	if n := s.seedFromDisk(filepath.Join(t.TempDir(), "does-not-exist")); n != 0 {
		t.Fatalf("seeded %d from missing dir, want 0", n)
	}
}
