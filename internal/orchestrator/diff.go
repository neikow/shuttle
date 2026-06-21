package orchestrator

import (
	"github.com/neikow/shuttle/internal/config"
)

// Action is what should happen to a service.
type Action string

const (
	ActionDeploy Action = "deploy"
	ActionNoop   Action = "noop"
)

// Step represents one reconciliation action.
type Step struct {
	Host    string
	Service string
	Action  Action
	SHA     string
}

// Plan is the set of steps needed to converge desired → actual.
type Plan struct {
	Steps []Step
}

// CurrentState holds what is currently deployed per service.
type CurrentState map[string]string // service → deployed SHA

// ComputePlan diffs the desired repo state against current and returns the steps to converge.
func ComputePlan(repo *config.Repo, current CurrentState, desiredSHA string) Plan {
	var steps []Step
	for _, svc := range repo.Services {
		if svc.IsExternal() {
			continue // external services are routed, never deployed
		}
		currentSHA, exists := current[svc.Name]
		if !exists || currentSHA != desiredSHA {
			steps = append(steps, Step{
				Host:    svc.Host,
				Service: svc.Name,
				Action:  ActionDeploy,
				SHA:     desiredSHA,
			})
		}
	}
	return Plan{Steps: steps}
}
