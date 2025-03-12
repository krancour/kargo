package health

import (
	"context"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// Checker is an interface for components that implement the logic for execution
// of a health check.
type Checker interface {
	// Name returns the name of the Checker.
	Name() string
	// Check executes a health check.
	Check(context.Context, Criteria) Result
}

// Criteria describes a request for the execution of a health check by a
// specific Checker.
type Criteria struct {
	// Project is the name of the Project that the health check requested by this
	// Criteria is associated with.
	Project string
	// Stage is the name of the Stage that the health check requested by this
	// Criteria is associated with.
	Stage string
	// Kind identifies a registered Checker that implements the logic
	// for the health check process.
	Kind string
	// Input is an opaque map of values to be passed to the Checker.
	Input Input
}

// Result represents the results of a health check executed by a Checker.
type Result struct {
	// Status is the high-level outcome of the HealthCheck executed by a
	// Checker.
	Status kargoapi.HealthState
	// Output is the opaque output of a HealthCheck executed by a
	// Checker. The Engine will aggregate this output and include it
	// in the final results of the health check, which will ultimately be included
	// in StageStatus.
	Output map[string]any
	// Issues is a list of issues that were encountered during the execution of
	// the HealthCheck by a Checker.
	Issues []string
}
