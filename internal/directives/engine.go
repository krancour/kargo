package directives

import (
	"context"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/internal/controller/health"
)

// Engine is an interface for executing user-defined promotion processes as well
// as corresponding health check processes.
type Engine interface {
	// Promote executes the provided list of PromotionSteps in sequence and
	// returns a PromotionResult that aggregates the results of all steps.
	Promote(context.Context, PromotionContext, []PromotionStep) (PromotionResult, error)
	// CheckHealth executes the specified health checks in sequence and returns a
	// kargoapi.Health that aggregates their results.
	CheckHealth(ctx context.Context, project, stage string, criteria []health.Criteria) kargoapi.Health
}
