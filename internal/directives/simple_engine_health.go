package directives

import (
	"context"
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/internal/controller/health"
	"github.com/akuity/kargo/internal/controller/promotion"
)

// CheckHealth implements the Engine interface.
func (e *SimpleEngine) CheckHealth(
	ctx context.Context,
	project string,
	stage string,
	checks []health.Criteria,
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
func (e *SimpleEngine) executeHealthChecks(
	ctx context.Context,
	project string,
	stage string,
	checks []health.Criteria,
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
func (e *SimpleEngine) executeHealthCheck(
	ctx context.Context,
	project string,
	stage string,
	criteria health.Criteria,
) health.Result {
	checker := e.registry.getHealthChecker(criteria.Kind)
	if checker == nil {
		return health.Result{
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
