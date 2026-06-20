package agent

import (
	"reflect"
	"testing"
)

func TestParseProjectVolumes(t *testing.T) {
	js := []byte(`{
		"name": "db",
		"volumes": {
			"pgdata": {"name": "db_pgdata"},
			"cache": {}
		}
	}`)
	got, err := parseProjectVolumes(js, nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []projectVolume{
		{Key: "cache", Name: "db_cache"}, // no explicit name => <project>_<key>
		{Key: "pgdata", Name: "db_pgdata"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Filter restricts to requested keys.
	filtered, err := parseProjectVolumes(js, []string{"pgdata"})
	if err != nil {
		t.Fatalf("parse filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Key != "pgdata" {
		t.Fatalf("filter failed: %+v", filtered)
	}
}

func TestResticRepoLocation(t *testing.T) {
	tests := []struct {
		target   string
		wantRepo string
		wantMnt  string
	}{
		{"s3:s3.amazonaws.com/bucket", "s3:s3.amazonaws.com/bucket", ""},
		{"b2:bucket:path", "b2:bucket:path", ""},
		{"/var/backups/restic", mountRepo, "/var/backups/restic"},
		{"local:/srv/restic", mountRepo, "/srv/restic"},
		{"restic:s3:s3.amazonaws.com/bucket", "s3:s3.amazonaws.com/bucket", ""},
		{"sftp:user@host:/srv/repo", "sftp:user@host:/srv/repo", ""},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			repo, mnt := resticRepoLocation(tt.target)
			if repo != tt.wantRepo || mnt != tt.wantMnt {
				t.Fatalf("location(%q) = (%q,%q), want (%q,%q)", tt.target, repo, mnt, tt.wantRepo, tt.wantMnt)
			}
		})
	}
}

func TestPgDumpCmd(t *testing.T) {
	if got := pgDumpCmd("postgres", ""); !reflect.DeepEqual(got, []string{"pg_dumpall", "-U", "postgres"}) {
		t.Fatalf("dumpall: %v", got)
	}
	if got := pgDumpCmd("app", "appdb"); !reflect.DeepEqual(got, []string{"pg_dump", "-U", "app", "appdb"}) {
		t.Fatalf("dump one: %v", got)
	}
	if got := pgDumpCmd("", ""); !reflect.DeepEqual(got, []string{"pg_dumpall"}) {
		t.Fatalf("dumpall no user: %v", got)
	}
}

func TestPgRestoreCmd(t *testing.T) {
	if got := pgRestoreCmd("postgres", ""); !reflect.DeepEqual(got, []string{"psql", "-U", "postgres"}) {
		t.Fatalf("restore all: %v", got)
	}
	if got := pgRestoreCmd("app", "appdb"); !reflect.DeepEqual(got, []string{"psql", "-U", "app", "-d", "appdb"}) {
		t.Fatalf("restore one: %v", got)
	}
}

func TestResticForgetArgs(t *testing.T) {
	if got := resticForgetArgs(BackupRetention{}); len(got) != 0 {
		t.Fatalf("empty retention should yield no args, got %v", got)
	}
	got := resticForgetArgs(BackupRetention{KeepLast: 3, KeepDaily: 7, KeepWeekly: 4})
	want := []string{"--keep-last", "3", "--keep-daily", "7", "--keep-weekly", "4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseResticSummary(t *testing.T) {
	out := `{"message_type":"status","percent_done":0.5}
{"message_type":"summary","snapshot_id":"abc123","total_bytes_processed":2048,"files_new":3}`
	sid, n, ok := parseResticSummary([]byte(out))
	if !ok || sid != "abc123" || n != 2048 {
		t.Fatalf("parse = (%q,%d,%v)", sid, n, ok)
	}

	if _, _, ok := parseResticSummary([]byte(`{"message_type":"status"}`)); ok {
		t.Fatal("no summary line should report ok=false")
	}
}

func TestDockerEnvFlags(t *testing.T) {
	flags, env := dockerEnvFlags(map[string]string{"RESTIC_PASSWORD": "s3cr3t", "AWS_ACCESS_KEY_ID": "AKIA"})
	// Sorted by key: AWS_ACCESS_KEY_ID before RESTIC_PASSWORD.
	wantFlags := []string{"-e", "AWS_ACCESS_KEY_ID", "-e", "RESTIC_PASSWORD"}
	if !reflect.DeepEqual(flags, wantFlags) {
		t.Fatalf("flags = %v, want %v", flags, wantFlags)
	}
	// Secrets land in the process env (not the argv).
	var found int
	for _, e := range env {
		if e == "RESTIC_PASSWORD=s3cr3t" || e == "AWS_ACCESS_KEY_ID=AKIA" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("creds missing from process env: %d/2", found)
	}
	for _, f := range flags {
		if f == "s3cr3t" || f == "AKIA" {
			t.Fatal("secret value leaked into argv flags")
		}
	}

	if flags, env := dockerEnvFlags(nil); flags != nil || env != nil {
		t.Fatal("nil env should yield nil flags and nil env")
	}
}

func TestSanitizeFile(t *testing.T) {
	if got := sanitizeFile("pg/data:1"); got != "pg_data_1" {
		t.Fatalf("sanitize = %q", got)
	}
}
