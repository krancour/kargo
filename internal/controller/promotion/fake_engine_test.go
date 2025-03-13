package promotion

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/internal/controller/health"
)

func TestFakeEngine_Promote(t *testing.T) {
	t.Run("without function injection", func(t *testing.T) {
		engine := &FakeEngine{}
		res, err := engine.Promote(context.Background(), Context{}, nil)
		assert.NoError(t, err)
		assert.Equal(t, kargoapi.PromotionPhaseSucceeded, res.Status)
	})

	t.Run("with function injection", func(t *testing.T) {
		ctx := context.Background()
		promoCtx := Context{
			Stage: "foo",
		}
		steps := []Step{{Kind: "mock"}}

		engine := &FakeEngine{
			ExecuteFn: func(
				givenCtx context.Context,
				givenPromoCtx Context,
				givenSteps []Step,
			) (Result, error) {
				assert.Equal(t, ctx, givenCtx)
				assert.Equal(t, promoCtx, givenPromoCtx)
				assert.Equal(t, steps, givenSteps)
				return Result{Status: kargoapi.PromotionPhaseErrored},
					errors.New("something went wrong")
			},
		}
		res, err := engine.Promote(ctx, promoCtx, steps)
		assert.ErrorContains(t, err, "something went wrong")
		assert.Equal(t, kargoapi.PromotionPhaseErrored, res.Status)
	})
}

func TestFakeEngine_CheckHealth(t *testing.T) {
	t.Run("without function injection", func(t *testing.T) {
		engine := &FakeEngine{}
		res := engine.CheckHealth(context.Background(), "fake-project", "fake-stage", nil)
		assert.Equal(t, kargoapi.HealthStateHealthy, res.Status)
	})

	t.Run("with function injection", func(t *testing.T) {
		ctx := context.Background()
		const testProject = "fake-project"
		const testStage = "fake-stage"
		criteria := []health.Criteria{{Kind: "mock"}}
		engine := &FakeEngine{
			CheckHealthFn: func(givenCtx context.Context, _, _ string, givenCriteria []health.Criteria) kargoapi.Health {
				assert.Equal(t, ctx, givenCtx)
				assert.Equal(t, criteria, givenCriteria)
				return kargoapi.Health{Status: kargoapi.HealthStateUnhealthy}
			},
		}
		res := engine.CheckHealth(ctx, testProject, testStage, criteria)
		assert.Equal(t, kargoapi.HealthStateUnhealthy, res.Status)
	})
}
