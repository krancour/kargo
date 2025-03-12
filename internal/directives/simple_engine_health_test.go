package directives

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func TestSimpleEngine_CheckHealth(t *testing.T) {
	tests := []struct {
		name       string
		checks     []HealthCheck
		assertions func(*testing.T, kargoapi.Health)
	}{
		{
			name: "successful health check",
			checks: []HealthCheck{
				{Kind: "success-check"},
			},
			assertions: func(t *testing.T, health kargoapi.Health) {
				assert.Equal(t, kargoapi.HealthStateHealthy, health.Status)
				assert.Empty(t, health.Issues)
				assert.NotNil(t, health.Output)
				assert.JSONEq(t, `[{"test":"success"}]`, string(health.Output.Raw))
			},
		},
		{
			name: "multiple successful health checks",
			checks: []HealthCheck{
				{Kind: "success-check"},
				{Kind: "success-check"},
			},
			assertions: func(t *testing.T, health kargoapi.Health) {
				assert.Equal(t, kargoapi.HealthStateHealthy, health.Status)
				assert.Empty(t, health.Issues)
				assert.NotNil(t, health.Output)
				assert.JSONEq(t, `[{"test":"success"},{"test":"success"}]`, string(health.Output.Raw))
			},
		},
		{
			name: "failed health check",
			checks: []HealthCheck{
				{Kind: "error-check"},
			},
			assertions: func(t *testing.T, health kargoapi.Health) {
				assert.Equal(t, kargoapi.HealthStateUnhealthy, health.Status)
				assert.Contains(t, health.Issues, "health check failed")
				assert.NotNil(t, health.Output)
			},
		},
		{
			name: "context cancellation",
			checks: []HealthCheck{
				{Kind: "context-waiter"},
			},
			assertions: func(t *testing.T, health kargoapi.Health) {
				assert.Equal(t, kargoapi.HealthStateUnknown, health.Status)
				assert.Contains(t, health.Issues, context.Canceled.Error())
				assert.Nil(t, health.Output)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			testRegistry := stepRunnerRegistry{}
			testRegistry.register(&mockHealthCheckRunner{
				name: "success-check",
				checkResult: HealthCheckResult{
					Status: kargoapi.HealthStateHealthy,
					Output: State{"test": "success"},
				},
			})
			testRegistry.register(&mockHealthCheckRunner{
				name: "error-check",
				checkResult: HealthCheckResult{
					Status: kargoapi.HealthStateUnhealthy,
					Issues: []string{"health check failed"},
					Output: State{"test": "error"},
				},
			})
			testRegistry.register(&mockHealthCheckRunner{
				name: "context-waiter",
				checkFunc: func(ctx context.Context, _, _ string, _ Config) HealthCheckResult {
					cancel()
					<-ctx.Done()
					return HealthCheckResult{
						Status: kargoapi.HealthStateUnknown,
						Issues: []string{ctx.Err().Error()},
					}
				},
			})

			engine := &SimpleEngine{
				registry: testRegistry,
			}

			health := engine.CheckHealth(ctx, "fake-project", "fake-stage", tt.checks)
			tt.assertions(t, health)
		})
	}
}

func TestSimpleEngine_executeHealthChecks(t *testing.T) {
	tests := []struct {
		name       string
		checks     []HealthCheck
		assertions func(*testing.T, kargoapi.HealthState, []string, []State)
	}{
		{
			name: "aggregate multiple healthy checks",
			checks: []HealthCheck{
				{Kind: "success-check"},
				{Kind: "success-check"},
			},
			assertions: func(t *testing.T, status kargoapi.HealthState, issues []string, output []State) {
				assert.Equal(t, kargoapi.HealthStateHealthy, status)
				assert.Empty(t, issues)
				assert.Len(t, output, 2)
				for _, o := range output {
					assert.Equal(t, "success", o["test"])
				}
			},
		},
		{
			name: "merge different health states",
			checks: []HealthCheck{
				{Kind: "success-check"},
				{Kind: "error-check"},
			},
			assertions: func(t *testing.T, status kargoapi.HealthState, issues []string, output []State) {
				assert.Equal(t, kargoapi.HealthStateUnhealthy, status)
				assert.Contains(t, issues, "health check failed")
				assert.Len(t, output, 2)
			},
		},
		{
			name: "context cancellation",
			checks: []HealthCheck{
				{Kind: "context-waiter"},
				{Kind: "success-check"}, // Should not execute
			},
			assertions: func(t *testing.T, status kargoapi.HealthState, issues []string, output []State) {
				assert.Equal(t, kargoapi.HealthStateUnknown, status)
				assert.Contains(t, issues, context.Canceled.Error())
				assert.Empty(t, output)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			testRegistry := stepRunnerRegistry{}
			testRegistry.register(&mockHealthCheckRunner{
				name: "success-check",
				checkResult: HealthCheckResult{
					Status: kargoapi.HealthStateHealthy,
					Output: State{"test": "success"},
				},
			})
			testRegistry.register(&mockHealthCheckRunner{
				name: "error-check",
				checkResult: HealthCheckResult{
					Status: kargoapi.HealthStateUnhealthy,
					Issues: []string{"health check failed"},
					Output: State{"test": "error"},
				},
			})
			testRegistry.register(&mockHealthCheckRunner{
				name: "context-waiter",
				checkFunc: func(ctx context.Context, _, _ string, _ Config) HealthCheckResult {
					cancel()
					<-ctx.Done()
					return HealthCheckResult{
						Status: kargoapi.HealthStateUnknown,
						Issues: []string{ctx.Err().Error()},
					}
				},
			})

			engine := &SimpleEngine{
				registry: testRegistry,
			}

			status, issues, output := engine.executeHealthChecks(ctx, "fake-project", "fake-stage", tt.checks)
			tt.assertions(t, status, issues, output)
		})
	}
}

func TestSimpleEngine_executeHealthCheck(t *testing.T) {
	tests := []struct {
		name       string
		check      HealthCheck
		assertions func(*testing.T, HealthCheckResult)
	}{
		{
			name:  "successful execution",
			check: HealthCheck{Kind: "success-check"},
			assertions: func(t *testing.T, result HealthCheckResult) {
				assert.Equal(t, kargoapi.HealthStateHealthy, result.Status)
				assert.Empty(t, result.Issues)
			},
		},
		{
			name:  "unregistered runner",
			check: HealthCheck{Kind: "unknown"},
			assertions: func(t *testing.T, result HealthCheckResult) {
				assert.Equal(t, kargoapi.HealthStateUnknown, result.Status)
				assert.Contains(t, result.Issues[0], "no promotion step runner registered for step kind")
				assert.Contains(t, result.Issues[0], "unknown")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testRegistry := stepRunnerRegistry{}
			testRegistry.register(&mockHealthCheckRunner{
				name: "success-check",
				checkResult: HealthCheckResult{
					Status: kargoapi.HealthStateHealthy,
				},
			})

			engine := &SimpleEngine{
				registry: testRegistry,
			}

			result := engine.executeHealthCheck(context.Background(), "fake-project", "fake-stage", tt.check)
			tt.assertions(t, result)
		})
	}
}
