package orchestrator

import (
	"context"
	"fmt"
	"sort"

	"github.com/neikow/shuttle/internal/config"
)

// PlanAction is what a `plan` would do to a service.
type PlanAction string

const (
	PlanCreate    PlanAction = "create"    // in the repo, never deployed
	PlanUpdate    PlanAction = "update"    // deployed, but at a different SHA
	PlanUnchanged PlanAction = "unchanged" // deployed at the desired SHA
	PlanRemove    PlanAction = "remove"    // deployed, but gone from the repo
)

// PlanEntry is the planned action for one service.
type PlanEntry struct {
	Service    string     `json:"service"`
	Host       string     `json:"host,omitempty"`
	Action     PlanAction `json:"action"`
	CurrentSHA string     `json:"current_sha,omitempty"`
	DesiredSHA string     `json:"desired_sha,omitempty"`
}

// PlanReport is the full desired-vs-actual diff at a repo SHA. It is read-only:
// computing it dispatches nothing and records nothing.
type PlanReport struct {
	SHA      string      `json:"sha"`
	Services []PlanEntry `json:"services"`
}

// Plan syncs the repo and diffs every service against the ledger's current
// SHAs, returning the actions a reconcile would take — without taking them.
// When the syncer has no ledger (e.g. a CI-style local plan with no data dir),
// every repo service shows as "create".
func (g *GitSyncer) Plan(ctx context.Context) (PlanReport, error) {
	repo, sha, err := g.syncAndLoad(ctx)
	if err != nil {
		return PlanReport{}, err
	}
	return g.planFrom(ctx, repo, sha)
}

// PlanRef is Plan against an arbitrary git ref (branch, tag, refs/pull/N/head,
// or SHA) instead of the configured branch HEAD, so CI can preview the exact PR
// branch's diff against the live ledger. ref == "" falls back to Plan. The ref
// is checked out in isolation, leaving the orchestrator's working tree intact.
func (g *GitSyncer) PlanRef(ctx context.Context, ref string) (PlanReport, error) {
	if ref == "" {
		return g.Plan(ctx)
	}
	sib, cleanup, err := g.checkoutRef(ctx, ref)
	if err != nil {
		return PlanReport{}, err
	}
	defer cleanup()
	repo, sha, err := sib.loadHead(ctx)
	if err != nil {
		return PlanReport{}, err
	}
	// Diff against the parent's live ledger, not the throwaway sibling's.
	return g.planFrom(ctx, repo, sha)
}

// planFrom builds the report for an already-loaded repo+SHA against the ledger's
// current SHAs. With no ledger every service shows as "create".
func (g *GitSyncer) planFrom(ctx context.Context, repo *config.Repo, sha string) (PlanReport, error) {
	current := map[string]string{}
	if g.store != nil {
		cur, err := g.store.CurrentSHAs(ctx)
		if err != nil {
			return PlanReport{}, fmt.Errorf("current state: %w", err)
		}
		current = cur
	}
	return buildPlanReport(repo, current, sha), nil
}

// buildPlanReport diffs the repo's services against the current (ledger) SHAs at
// the desired SHA. Pure: no git, no I/O.
func buildPlanReport(repo *config.Repo, current map[string]string, sha string) PlanReport {
	report := PlanReport{SHA: sha}
	inRepo := make(map[string]bool, len(repo.Services))
	for _, svc := range repo.Services {
		inRepo[svc.Name] = true
		cur, deployed := current[svc.Name]
		action := PlanUnchanged
		switch {
		case !deployed:
			action = PlanCreate
		case cur != sha:
			action = PlanUpdate
		}
		report.Services = append(report.Services, PlanEntry{
			Service: svc.Name, Host: svc.Host, Action: action,
			CurrentSHA: cur, DesiredSHA: sha,
		})
	}
	// Services in the ledger but absent from the repo would be torn down.
	for svc, cur := range current {
		if !inRepo[svc] {
			report.Services = append(report.Services, PlanEntry{
				Service: svc, Action: PlanRemove, CurrentSHA: cur,
			})
		}
	}
	sort.Slice(report.Services, func(i, j int) bool {
		return report.Services[i].Service < report.Services[j].Service
	})
	return report
}
