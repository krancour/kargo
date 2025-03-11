package directives

import (
	"context"

	"github.com/akuity/kargo/pkg/x/directive"
)

// HealthCheckStepRunner is an interface for components that implement the logic
// for execution of the individual HealthCheckSteps.
type HealthCheckStepRunner interface {
	// Name returns the name of the HealthCheckStepRunner.
	Name() string
	// RunHealthCheckStep executes a health check using the provided
	// directive.HealthCheckStepContext.
	RunHealthCheckStep(context.Context, *directive.HealthCheckStepContext) directive.HealthCheckStepResult
}
