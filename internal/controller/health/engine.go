package health

import (
	"context"
	"encoding/json"
	"fmt"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/internal/controller/promotion"
)

// Engine is an interface for executing a sequence of health checks.
type Engine interface {
	// Check executes the specified sequence of health checks and returns a
	// kargoapi.Health that aggregates their results.
	Check(ctx context.Context, project, stage string, criteria []Criteria) kargoapi.Health
}

// simpleEngine is a simple implementation of the Engine interface that uses
// built-in Checkers.
type simpleEngine struct {
	registry checkerRegistry
}

// NewSimpleEngine returns a simple implementation of the Engine interface that
// uses built-in Checkers.
func NewSimpleEngine() Engine {
	return &simpleEngine{registry: checkerReg}
}

// CheckHealth implements the Engine interface.
func (e *simpleEngine) Check(
	ctx context.Context,
	project string,
	stage string,
	checks []Criteria,
) kargoapi.Health {
	status, issues, output := e.executeHealthChecks(ctx, project, stage, checks)
	if len(output) == 0 {
		return kargoapi.Health{
			Status: status,
			Issues: issues,
		}
	}

	b, err := json.Marshal(output)
	if err != nil {
		issues = append(issues, fmt.Sprintf("failed to marshal health output: %s", err.Error()))
	}

	return kargoapi.Health{
		Status: status,
		Issues: issues,
		Output: &apiextensionsv1.JSON{Raw: b},
	}
}

// executeHealthChecks executes a list of HealthChecks in sequence.
func (e *simpleEngine) executeHealthChecks(
	ctx context.Context,
	project string,
	stage string,
	checks []Criteria,
) (kargoapi.HealthState, []string, []promotion.State) {
	var (
		aggregatedStatus = kargoapi.HealthStateHealthy
		aggregatedIssues []string
		aggregatedOutput = make([]promotion.State, 0, len(checks))
	)

	for _, check := range checks {
		select {
		case <-ctx.Done():
			aggregatedStatus = aggregatedStatus.Merge(kargoapi.HealthStateUnknown)
			aggregatedIssues = append(aggregatedIssues, ctx.Err().Error())
			return aggregatedStatus, aggregatedIssues, aggregatedOutput
		default:
		}

		result := e.executeHealthCheck(ctx, project, stage, check)
		aggregatedStatus = aggregatedStatus.Merge(result.Status)
		aggregatedIssues = append(aggregatedIssues, result.Issues...)

		if result.Output != nil {
			aggregatedOutput = append(aggregatedOutput, result.Output)
		}
	}

	return aggregatedStatus, aggregatedIssues, aggregatedOutput
}

// executeHealthCheck executes a single HealthCheck.
func (e *simpleEngine) executeHealthCheck(
	ctx context.Context,
	project string,
	stage string,
	criteria Criteria,
) Result {
	checker := e.registry.getChecker(criteria.Kind)
	if checker == nil {
		return Result{
			Status: kargoapi.HealthStateUnknown,
			Issues: []string{
				fmt.Sprintf("no health checker registered for health check kind %q", criteria.Kind),
			},
		}
	}
	criteria.Project = project
	criteria.Stage = stage
	return checker.Check(ctx, criteria)
}
