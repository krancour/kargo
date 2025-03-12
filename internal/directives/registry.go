package directives

import (
	"github.com/akuity/kargo/internal/controller/health"
	"github.com/akuity/kargo/internal/controller/promotion"
)

// NamedRunner is an interface for runners that can self-report their name.
type NamedRunner interface {
	Name() string
}

// Register registers a NamedRunner with the package's internal step runner
// registry.
func Register(runner NamedRunner) {
	runnerReg.register(runner)
}

// runnerReg is a registry of PromotionStepRunner and HealthCheckRunner
// implementations.
var runnerReg = stepRunnerRegistry{}

// stepRunnerRegistry is a registry of named components that can presumably
// execute some sort of step, like a step of a promotion process or a health
// check process.
type stepRunnerRegistry map[string]NamedRunner

// register adds a named component to the stepRunnerRegistry.
func (s stepRunnerRegistry) register(runner NamedRunner) {
	s[runner.Name()] = runner
}

// getPromotionStepRunner returns the PromotionStepRunner for the promotion step
// with the given name, if no runner is registered with the given name or the
// runner with the given name does not implement PromotionStepRunner, nil is
// returned.
func (s stepRunnerRegistry) getPromotionStepRunner(name string) promotion.StepRunner {
	runner, ok := s[name]
	if !ok {
		return nil
	}
	promoStepRunner, ok := runner.(promotion.StepRunner)
	if !ok {
		return nil
	}
	return promoStepRunner
}

// getHealthChecker returns the health.Checker registered with the given name,
// or an error if no such health.Checker is registered.
func (s stepRunnerRegistry) getHealthChecker(name string) health.Checker {
	runner, ok := s[name]
	if !ok {
		return nil
	}
	healthChecker, ok := runner.(health.Checker)
	if !ok {
		return nil
	}
	return healthChecker
}
