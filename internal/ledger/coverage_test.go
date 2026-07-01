package ledger

import (
	"context"
	"testing"
	"time"
)

func TestCurrentSHAs(t *testing.T) {
	s := openMemory(t)
	ctx := context.Background()
	// Two successful deploys for web (latest wins) + one for api + a failed one.
	rec := func(id, svc, sha string, st Status, ts time.Time) {
		if err := s.RecordDeploy(ctx, DeployRecord{DeployID: id, Service: svc, Host: "h", SHA: sha, Status: st, TriggeredBy: TriggeredByManual, StartedAt: ts}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now()
	rec("1", "web", "old", StatusSuccess, base)
	rec("2", "web", "new", StatusSuccess, base.Add(time.Minute))
	rec("3", "api", "a1", StatusSuccess, base)
	rec("4", "db", "x", StatusFailed, base)

	cur, err := s.CurrentSHAs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cur["web"] != "new" {
		t.Errorf("web sha = %q, want new (latest success)", cur["web"])
	}
	if cur["api"] != "a1" {
		t.Errorf("api sha = %q, want a1", cur["api"])
	}
	if _, ok := cur["db"]; ok {
		t.Error("a failed-only service should not appear in CurrentSHAs")
	}
}

func TestErrorMessages(t *testing.T) {
	if e := (ErrWebhookNotFound{ID: "abc"}); e.Error() == "" {
		t.Error("ErrWebhookNotFound.Error() should be non-empty")
	}
	if e := (ErrControlTokenNotFound{}); e.Error() == "" {
		t.Error("ErrControlTokenNotFound.Error() should be non-empty")
	}
}
