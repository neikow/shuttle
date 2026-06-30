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

func TestDeployedSet_PutRemove(t *testing.T) {
	s := newDeployedSet()
	s.put("web", "/work/web", "sha1")
	s.put("api", "/work/api", "sha2")
	if snap := s.snapshot(); len(snap) != 2 || snap["web"].sha != "sha1" || snap["api"].workDir != "/work/api" {
		t.Fatalf("after put: %+v", s.snapshot())
	}
	// put updates in place (same key), not a duplicate.
	s.put("web", "/work/web", "sha1b")
	if snap := s.snapshot(); len(snap) != 2 || snap["web"].sha != "sha1b" {
		t.Fatalf("after re-put: %+v", s.snapshot())
	}
	// snapshot is a copy: mutating it doesn't affect the set.
	snap := s.snapshot()
	delete(snap, "web")
	if _, ok := s.snapshot()["web"]; !ok {
		t.Error("snapshot must be a copy; mutating it changed the set")
	}
	s.remove("web")
	if snap := s.snapshot(); len(snap) != 1 {
		t.Fatalf("after remove: %+v", snap)
	}
	if _, ok := s.snapshot()["web"]; ok {
		t.Error("web should be removed")
	}
	s.remove("nonexistent") // no-op, must not panic
}

func TestSeedFromDisk_missingBaseDir(t *testing.T) {
	s := newDeployedSet()
	if n := s.seedFromDisk(filepath.Join(t.TempDir(), "does-not-exist")); n != 0 {
		t.Fatalf("seeded %d from missing dir, want 0", n)
	}
}
