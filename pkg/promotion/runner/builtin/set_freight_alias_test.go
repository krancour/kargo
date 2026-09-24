package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libFreight "github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
	"github.com/akuity/kargo/pkg/promotion"
	"github.com/akuity/kargo/pkg/x/promotion/runner/builtin"
)

func Test_setFreightAlias_convert(t *testing.T) {
	tests := []validationTestCase{
		{
			name:   "both name and alias are not specified",
			config: promotion.Config{},
			expectedProblems: []string{
				"(root): name is required",
				"(root): alias is required",
			},
		},
		{
			name: "name is not specified",
			config: promotion.Config{
				"alias": "stable",
			},
			expectedProblems: []string{
				"(root): name is required",
			},
		},
		{
			name: "name is empty",
			config: promotion.Config{
				"name":  "",
				"alias": "new-alias",
			},
			expectedProblems: []string{
				"name: String length must be greater than or equal to 1",
			},
		},
		{
			name: "alias is not specified",
			config: promotion.Config{
				"name": "fake-freight-id",
			},
			expectedProblems: []string{
				"(root): alias is required",
			},
		},
		{
			name: "alias is empty",
			config: promotion.Config{
				"name":  "fake-freight-id",
				"alias": "",
			},
			expectedProblems: []string{
				"alias: String length must be greater than or equal to 1",
			},
		},
		{
			name: "unknown field is not allowed",
			config: promotion.Config{
				"name":       "fake-freight-id",
				"alias":      "new-alias",
				"unexpected": "nope",
			},
			expectedProblems: []string{
				"(root): Additional property unexpected is not allowed",
			},
		},
		{
			name: "valid minimal config",
			config: promotion.Config{
				"name":  "fake-freight-id",
				"alias": "new-alias",
			},
		},
	}

	r := newSetFreightAlias(promotion.StepRunnerCapabilities{})
	runner, ok := r.(*freightAliasSetter)
	require.True(t, ok)

	runValidationTests(t, runner.convert, tests)
}

func Test_setFreightAlias_run(t *testing.T) {
	const (
		testFreight  = "freight-id-1"
		otherFreight = "freight-id-2"
		testProject  = "test-project"
		oldAlias     = "old-alias"
		alias        = "new-alias"
	)

	newTestFreight := func() *kargoapi.Freight {
		return &kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testFreight,
				Namespace: testProject,
				Labels: map[string]string{
					kargoapi.LabelKeyAlias: oldAlias,
				},
			},
			Alias: oldAlias,
		}
	}

	tests := []struct {
		name       string
		store      func() *memory.Store
		cfg        builtin.SetFreightAliasConfig
		assertions func(*testing.T, promotion.StepResult, *memory.Store, error)
	}{
		{
			name:  "freight not found",
			store: func() *memory.Store { return memory.NewStore() },
			cfg: builtin.SetFreightAliasConfig{
				Name:  testFreight,
				Alias: alias,
			},
			assertions: func(t *testing.T, res promotion.StepResult, _ *memory.Store, err error) {
				require.ErrorContains(t, err, "not found")
				require.ErrorContains(t, err, testFreight)
				require.Equal(t, kargoapi.PromotionStepStatusFailed, res.Status)
				var terminalErr *promotion.TerminalError
				require.ErrorAs(t, err, &terminalErr)
			},
		},
		{
			name: "get error",
			store: func() *memory.Store {
				s := memory.NewStore(newTestFreight())
				s.Hooks.Get = func(context.Context, string, string) error {
					return errors.New("something went wrong")
				}
				return s
			},
			cfg: builtin.SetFreightAliasConfig{
				Name:  testFreight,
				Alias: alias,
			},
			assertions: func(t *testing.T, res promotion.StepResult, _ *memory.Store, err error) {
				require.ErrorContains(t, err, "failed to fetch Freight")
				require.ErrorContains(t, err, testFreight)
				require.ErrorContains(t, err, "something went wrong")
				require.Equal(t, kargoapi.PromotionStepStatusFailed, res.Status)
				var terminalErr *promotion.TerminalError
				require.ErrorAs(t, err, &terminalErr)
			},
		},
		{
			name: "update error",
			store: func() *memory.Store {
				s := memory.NewStore(newTestFreight())
				s.Hooks.UpdateAlias = func(context.Context, string, string, string) error {
					return errors.New("something went wrong")
				}
				return s
			},
			cfg: builtin.SetFreightAliasConfig{
				Name:  testFreight,
				Alias: alias,
			},
			assertions: func(t *testing.T, res promotion.StepResult, _ *memory.Store, err error) {
				require.ErrorContains(t, err, "failed to update alias")
				require.ErrorContains(t, err, testFreight)
				require.ErrorContains(t, err, "something went wrong")
				require.Equal(t, kargoapi.PromotionStepStatusFailed, res.Status)
				var terminalErr *promotion.TerminalError
				require.ErrorAs(t, err, &terminalErr)
			},
		},
		{
			name: "alias conflict",
			store: func() *memory.Store {
				return memory.NewStore(
					newTestFreight(),
					&kargoapi.Freight{
						ObjectMeta: metav1.ObjectMeta{
							Name:      otherFreight,
							Namespace: testProject,
							Labels: map[string]string{
								kargoapi.LabelKeyAlias: alias,
							},
						},
						Alias: alias,
					},
				)
			},
			cfg: builtin.SetFreightAliasConfig{
				Name:  testFreight,
				Alias: alias,
			},
			assertions: func(t *testing.T, res promotion.StepResult, s *memory.Store, err error) {
				require.ErrorContains(t, err, "failed to update alias")
				require.Equal(t, kargoapi.PromotionStepStatusFailed, res.Status)
				var terminalErr *promotion.TerminalError
				require.ErrorAs(t, err, &terminalErr)
				require.ErrorIs(t, terminalErr.Err, libFreight.ErrAliasConflict)

				// The Freight's alias is unchanged
				f, getErr := s.Get(t.Context(), testProject, testFreight)
				require.NoError(t, getErr)
				require.Equal(t, oldAlias, f.Alias)
				require.Equal(t, oldAlias, f.Labels[kargoapi.LabelKeyAlias])
			},
		},
		{
			name:  "successful alias update",
			store: func() *memory.Store { return memory.NewStore(newTestFreight()) },
			cfg: builtin.SetFreightAliasConfig{
				Name:  testFreight,
				Alias: alias,
			},
			assertions: func(t *testing.T, res promotion.StepResult, s *memory.Store, err error) {
				require.NoError(t, err)
				require.Equal(t, kargoapi.PromotionStepStatusSucceeded, res.Status)
				require.Contains(t, res.Message, oldAlias)
				require.Contains(t, res.Message, alias)

				f, err := s.Get(t.Context(), testProject, testFreight)
				require.NoError(t, err)
				require.Equal(t, alias, f.Alias)
				require.Equal(t, alias, f.Labels[kargoapi.LabelKeyAlias])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.store()
			step := &freightAliasSetter{freightStore: store}

			stepCtx := &promotion.StepContext{
				Project: testProject,
			}

			res, err := step.run(t.Context(), stepCtx, tt.cfg)
			tt.assertions(t, res, store, err)
		})
	}
}
