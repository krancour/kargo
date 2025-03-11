package directive

import (
	"context"
	"time"
)

// StepRunner is an interface for components that can execute steps of a
// promotion or health check process.
type StepRunner interface {
	// Name returns the name of the PromotionStepRunner.
	Name() string
	// RunPromotionStep executes an individual step of a user-defined promotion
	// process using the provided directive.PromotionStepContext. Implementations may
	// indirectly modify that context through the returned PromotionStepResult to
	// allow subsequent promotion steps to access the results of its execution.
	RunPromotionStep(context.Context, *PromotionStepContext) (PromotionStepResult, error)
	// DefaultTimeout returns the default timeout for the step.
	DefaultTimeout() *time.Duration
	// DefaultErrorThreshold returns the number of consecutive times the step must
	// fail (for any reason) before retries are abandoned and the entire Promotion
	// is marked as failed.
	DefaultErrorThreshold() uint32
	// RunHealthCheckStep executes a health check using the provided
	// directive.HealthCheckStepContext.
	RunHealthCheckStep(context.Context, *HealthCheckStepContext) HealthCheckStepResult
}
