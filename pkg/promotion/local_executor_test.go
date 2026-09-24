package promotion

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/component"
	"github.com/akuity/kargo/pkg/credentials"
	"github.com/akuity/kargo/pkg/freight/memory"
)

func TestNewLocalStepExecutor(t *testing.T) {
	registry := MustNewStepRunnerRegistry(
		StepRunnerRegistration{
			Name: "fake-step",
			Value: func(_ StepRunnerCapabilities) StepRunner {
				return &MockStepRunner{}
			},
		},
	)

	kargoClient := fake.NewClientBuilder().Build()
	freightStore := memory.NewStore()
	argoCDClient := fake.NewClientBuilder().Build()
	credsDB := &credentials.FakeDB{}

	executor := NewLocalStepExecutor(
		registry,
		kargoClient,
		freightStore,
		argoCDClient,
		credsDB,
		nil,
	)

	require.NotNil(t, executor)
	require.IsType(t, &LocalStepExecutor{}, executor)
	require.Equal(t, registry, executor.registry)
	require.Equal(t, kargoClient, executor.kargoClient)
	require.Same(t, freightStore, executor.freightStore)
	require.Equal(t, argoCDClient, executor.argoCDClient)
	require.Equal(t, credsDB, executor.credsDB)
}

func TestLocalStepExecutor_ExecuteStep(t *testing.T) {
	tests := []struct {
		name       string
		registry   StepRunnerRegistry
		request    StepExecutionRequest
		assertions func(t *testing.T, result StepResult, err error)
	}{
		{
			name:     "no runner registered for step kind",
			registry: MustNewStepRunnerRegistry(),
			request: StepExecutionRequest{
				Context: StepContext{},
				Step: Step{
					Kind: "unknown-step",
				},
			},
			assertions: func(t *testing.T, result StepResult, err error) {
				require.Error(t, err)
				require.True(t, component.IsNotFoundError(err))
				require.Equal(t, StepResult{
					Status: kargoapi.PromotionStepStatusErrored,
				}, result)
			},
		},
		{
			name: "successful step execution",
			registry: MustNewStepRunnerRegistry(
				StepRunnerRegistration{
					Name: "test-step",
					Value: func(_ StepRunnerCapabilities) StepRunner {
						return &MockStepRunner{
							RunResult: StepResult{
								Status: kargoapi.PromotionStepStatusSucceeded,
							},
						}
					},
				},
			),
			request: StepExecutionRequest{
				Context: StepContext{},
				Step: Step{
					Kind: "test-step",
				},
			},
			assertions: func(t *testing.T, result StepResult, err error) {
				require.Equal(t, StepResult{
					Status: kargoapi.PromotionStepStatusSucceeded,
				}, result)
				require.NoError(t, err)
			},
		},
		{
			name: "step execution returns error",
			registry: MustNewStepRunnerRegistry(
				StepRunnerRegistration{
					Name: "test-step",
					Value: func(_ StepRunnerCapabilities) StepRunner {
						return &MockStepRunner{
							RunResult: StepResult{
								Status: kargoapi.PromotionStepStatusErrored,
							},
							RunErr: errors.New("step execution failed"),
						}
					},
				},
			),
			request: StepExecutionRequest{
				Context: StepContext{},
				Step: Step{
					Kind: "test-step",
				},
			},
			assertions: func(t *testing.T, result StepResult, err error) {
				require.Equal(t, StepResult{
					Status: kargoapi.PromotionStepStatusErrored,
				}, result)
				require.EqualError(
					t, err,
					`error running step "": step execution failed`,
				)
			},
		},
		{
			name: "step execution returns failed",
			registry: MustNewStepRunnerRegistry(
				StepRunnerRegistration{
					Name: "test-step",
					Value: func(_ StepRunnerCapabilities) StepRunner {
						return &MockStepRunner{
							RunResult: StepResult{
								Status: kargoapi.PromotionStepStatusFailed,
							},
							RunErr: errors.New("deployment blocked by policy"),
						}
					},
				},
			),
			request: StepExecutionRequest{
				Context: StepContext{},
				Step: Step{
					Kind: "test-step",
				},
			},
			assertions: func(t *testing.T, result StepResult, err error) {
				require.Equal(t, StepResult{
					Status: kargoapi.PromotionStepStatusFailed,
				}, result)
				require.EqualError(
					t, err,
					`step "": deployment blocked by policy`,
				)
			},
		},
		{
			name: "step execution panics",
			registry: MustNewStepRunnerRegistry(
				StepRunnerRegistration{
					Name: "test-step",
					Value: func(_ StepRunnerCapabilities) StepRunner {
						return &MockStepRunner{
							RunFunc: func(context.Context, *StepContext) (StepResult, error) {
								panic("step runner panicked")
							},
						}
					},
				},
			),
			request: StepExecutionRequest{
				Context: StepContext{},
				Step: Step{
					Kind: "test-step",
				},
			},
			assertions: func(t *testing.T, result StepResult, err error) {
				require.Equal(t, StepResult{
					Status: kargoapi.PromotionStepStatusErrored,
				}, result)
				require.ErrorContains(t, err, "step panicked: step runner panicked")

				var terminalErr *TerminalError
				require.ErrorAs(t, err, &terminalErr)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := NewLocalStepExecutor(tt.registry, nil, nil, nil, nil, nil)
			result, err := executor.ExecuteStep(t.Context(), tt.request)
			tt.assertions(t, result, err)
		})
	}
}

func TestLocalStepExecutor_ExecuteStep_capabilities(t *testing.T) {
	kargoClient := fake.NewClientBuilder().Build()
	freightStore := memory.NewStore()

	var gotCaps StepRunnerCapabilities
	registry := MustNewStepRunnerRegistry(
		StepRunnerRegistration{
			Name: "test-step",
			Metadata: StepRunnerMetadata{
				RequiredCapabilities: []StepRunnerCapability{
					StepCapabilityAccessControlPlane,
				},
			},
			Value: func(caps StepRunnerCapabilities) StepRunner {
				gotCaps = caps
				return &MockStepRunner{
					RunResult: StepResult{
						Status: kargoapi.PromotionStepStatusSucceeded,
					},
				}
			},
		},
	)

	executor := NewLocalStepExecutor(
		registry,
		kargoClient,
		freightStore,
		fake.NewClientBuilder().Build(),
		&credentials.FakeDB{},
		nil,
	)
	_, err := executor.ExecuteStep(
		t.Context(),
		StepExecutionRequest{Step: Step{Kind: "test-step"}},
	)
	require.NoError(t, err)
	require.Equal(t, kargoClient, gotCaps.KargoClient)
	require.Same(t, freightStore, gotCaps.FreightStore)
	require.Nil(t, gotCaps.ArgoCDClient)
	require.Nil(t, gotCaps.CredsDB)
}
