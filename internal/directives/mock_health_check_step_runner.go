package directives

import "context"

// mockHealthCheckRunner is a mock implementation of the HealthCheckRunner
// interface, which can be used for testing.
type mockHealthCheckRunner struct {
	// name is the name of the HealthCheckRunner.
	name string
	// checkFunc is the function that this runner should call when Check is
	// called. If set, this function will be called instead of returning
	// checkResult.
	checkFunc func(ctx context.Context, project, stage string, config Config) HealthCheckResult
	// checkResult is the result that this runner should return when Check is
	// called.
	checkResult HealthCheckResult
}

// Name implements the NamedRunner interface.
func (m *mockHealthCheckRunner) Name() string {
	return m.name
}

// Check implements the HealthCheckRunner interface.
func (m *mockHealthCheckRunner) Check(
	ctx context.Context,
	project,
	stage string,
	config Config,
) HealthCheckResult {
	if m.checkFunc != nil {
		return m.checkFunc(ctx, project, stage, config)
	}
	return m.checkResult
}
