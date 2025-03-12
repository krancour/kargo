package directives

import (
	"context"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// HealthCheckRunner is an interface for components that implement the logic
// for execution of a single HealthCheck.
type HealthCheckRunner interface {
	// Name returns the name of the HealthCheckRunner.
	Name() string
	// Check executes a health check.
	Check(ctx context.Context, stage, project string, config Config) HealthCheckResult
}

// HealthCheck describes a single health check process.
type HealthCheck struct {
	// Kind identifies a registered HealthCheckRunner that implements the logic
	// for the health check process.
	Kind string
	// Config is an opaque map of configuration values to be passed to the
	// HealthCheckRunner executing this step.
	Config Config
}

// HealthCheckResult represents the results of a single HealthCheck executed
// by a HealthCheckRunner.
type HealthCheckResult struct {
	// Status is the high-level outcome of the HealthCheck executed by a
	// HealthCheckRunner.
	Status kargoapi.HealthState
	// Output is the opaque output of a HealthCheck executed by a
	// HealthCheckRunner. The Engine will aggregate this output and include it
	// in the final results of the health check, which will ultimately be included
	// in StageStatus.
	Output map[string]any
	// Issues is a list of issues that were encountered during the execution of
	// the HealthCheck by a HealthCheckRunner.
	Issues []string
}
