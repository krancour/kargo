package directives

import (
	"context"
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// CheckHealth implements the Engine interface.
func (e *SimpleEngine) CheckHealth(
	ctx context.Context,
	project string,
	stage string,
	steps []HealthCheckStep,
) kargoapi.Health {
	status, issues, output := e.executeHealthChecks(ctx, project, stage, steps)
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

// executeHealthChecks executes a list of HealthCheckSteps in sequence.
func (e *SimpleEngine) executeHealthChecks(
	ctx context.Context,
	project string,
	stage string,
	steps []HealthCheckStep,
) (kargoapi.HealthState, []string, []State) {
	var (
		aggregatedStatus = kargoapi.HealthStateHealthy
		aggregatedIssues []string
		aggregatedOutput = make([]State, 0, len(steps))
	)

	for _, step := range steps {
		select {
		case <-ctx.Done():
			aggregatedStatus = aggregatedStatus.Merge(kargoapi.HealthStateUnknown)
			aggregatedIssues = append(aggregatedIssues, ctx.Err().Error())
			return aggregatedStatus, aggregatedIssues, aggregatedOutput
		default:
		}

		result := e.executeHealthCheck(ctx, project, stage, step)
		aggregatedStatus = aggregatedStatus.Merge(result.Status)
		aggregatedIssues = append(aggregatedIssues, result.Issues...)

		if result.Output != nil {
			aggregatedOutput = append(aggregatedOutput, result.Output)
		}
	}

	return aggregatedStatus, aggregatedIssues, aggregatedOutput
}

// executeHealthCheck executes a single HealthCheckStep.
func (e *SimpleEngine) executeHealthCheck(
	ctx context.Context,
	project string,
	stage string,
	step HealthCheckStep,
) HealthCheckStepResult {
	runner := e.registry.getHealthCheckStepRunner(step.Kind)
	if runner == nil {
		return HealthCheckStepResult{
			Status: kargoapi.HealthStateUnknown,
			Issues: []string{
				fmt.Sprintf("no promotion step runner registered for step kind %q", step.Kind),
			},
		}
	}
	return runner.RunHealthCheckStep(ctx, project, stage, step.Config.DeepCopy())
}
