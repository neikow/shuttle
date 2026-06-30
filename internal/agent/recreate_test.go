package agent

import (
	"context"
	"strings"
	"testing"
)

func TestComposeApply_Recreate(t *testing.T) {
	d, _ := scriptDriver(t, "echo started; exit 0")
	ch, err := d.Apply(context.Background(), ApplyParams{
		Service: "web", ComposeYAML: []byte("services: {}\n"), WorkDir: t.TempDir(),
		UpdatePolicy: "recreate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(drainText(ch), "\n"); strings.Contains(joined, "compose error") {
		t.Errorf("recreate apply errored:\n%s", joined)
	}
}

func TestComposeRollback(t *testing.T) {
	d, _ := scriptDriver(t, "exit 0")
	ch, err := d.Rollback(context.Background(), RollbackParams{
		Service: "web", ComposeYAML: []byte("services: {}\n"), WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(drainText(ch), "\n"); strings.Contains(joined, "compose error") {
		t.Errorf("rollback errored:\n%s", joined)
	}
}
