package builtin

import (
	"context"
	"errors"
	"fmt"

	"github.com/xeipuuv/gojsonschema"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/promotion"
	"github.com/akuity/kargo/pkg/x/promotion/runner/builtin"
)

const stepKindSetFreightAlias = "set-freight-alias"

func init() {
	promotion.DefaultStepRunnerRegistry.MustRegister(
		promotion.StepRunnerRegistration{
			Name: stepKindSetFreightAlias,
			Metadata: promotion.StepRunnerMetadata{
				RequiredCapabilities: []promotion.StepRunnerCapability{
					promotion.StepCapabilityAccessControlPlane,
				},
			},
			Value: newSetFreightAlias,
		},
	)
}

// setFreightAlias is an implementation of the promotion.StepRunner interface
// that updates aliases of Freight resources.
type freightAliasSetter struct {
	freightStore freight.Store
	schemaLoader gojsonschema.JSONLoader
}

// convert validates the configuration against a JSON schema and converts it
// into a builtin.SetFreightAliasConfig struct.
func (s *freightAliasSetter) convert(cfg promotion.Config) (builtin.SetFreightAliasConfig, error) {
	return validateAndConvert[builtin.SetFreightAliasConfig](s.schemaLoader, cfg, stepKindSetFreightAlias)
}

// newSetFreightAlias returns an implementation of the promotion.StepRunner
// interface that updates alias on Freight resources.
func newSetFreightAlias(caps promotion.StepRunnerCapabilities) promotion.StepRunner {
	return &freightAliasSetter{
		freightStore: caps.FreightStore,
		schemaLoader: getConfigSchemaLoader(stepKindSetFreightAlias),
	}
}

// Run implements the promotion.StepRunner interface.
func (s *freightAliasSetter) Run(
	ctx context.Context,
	stepCtx *promotion.StepContext,
) (promotion.StepResult, error) {
	cfg, err := s.convert(stepCtx.Config)
	if err != nil {
		return promotion.StepResult{
			Status: kargoapi.PromotionStepStatusFailed,
		}, &promotion.TerminalError{Err: err}
	}

	return s.run(ctx, stepCtx, cfg)
}

func (s *freightAliasSetter) run(
	ctx context.Context,
	stepCtx *promotion.StepContext,
	cfg builtin.SetFreightAliasConfig,
) (promotion.StepResult, error) {
	f, err := s.freightStore.Get(ctx, stepCtx.Project, cfg.Name)
	if err != nil {
		if errors.Is(err, freight.ErrNotFound) {
			return promotion.StepResult{
				Status: kargoapi.PromotionStepStatusFailed,
			}, &promotion.TerminalError{
				Err: fmt.Errorf("freight %q not found in project %q", cfg.Name, stepCtx.Project),
			}
		}
		return promotion.StepResult{
			Status: kargoapi.PromotionStepStatusFailed,
		}, &promotion.TerminalError{
			Err: fmt.Errorf("failed to fetch Freight %q in project %q: %w", cfg.Name, stepCtx.Project, err),
		}
	}

	// Alias uniqueness is enforced by the Store.
	if _, err = s.freightStore.UpdateAlias(ctx, stepCtx.Project, f.Name, cfg.Alias); err != nil {
		return promotion.StepResult{
			Status: kargoapi.PromotionStepStatusFailed,
		}, &promotion.TerminalError{
			Err: fmt.Errorf("failed to update alias for Freight %q in project %q: %w", f.Name, stepCtx.Project, err),
		}
	}

	return promotion.StepResult{
		Status: kargoapi.PromotionStepStatusSucceeded,
		Message: fmt.Sprintf(
			"updated alias of Freight %q from %q to %q",
			f.Name,
			f.Alias,
			cfg.Alias,
		),
	}, nil
}
