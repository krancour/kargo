package directives

import (
	"fmt"
	"testing"

	"github.com/akuity/kargo/internal/controller/health"
	"github.com/akuity/kargo/internal/controller/promotion"
	"github.com/stretchr/testify/assert"
)

func TestStepRunnerRegistry_register(t *testing.T) {
	t.Run("registers", func(t *testing.T) {
		registry := stepRunnerRegistry{}
		runner := &promotion.MockStepRunner{}
		registry.register(runner)
		assert.Same(t, runner, registry[runner.Name()])
	})

	t.Run("overwrites registration", func(t *testing.T) {
		registry := stepRunnerRegistry{}
		runner1 := &promotion.MockStepRunner{}
		registry.register(runner1)
		runner2 := &promotion.MockStepRunner{
			RunErr: fmt.Errorf("error"),
		}
		registry.register(runner2)
		assert.NotSame(t, runner1, registry[runner2.Name()])
		assert.Same(t, runner2, registry[runner2.Name()])
	})
}

func TestStepRunnerRegistry_getPromotionStepRunner(t *testing.T) {
	t.Run("registration exists", func(t *testing.T) {
		registry := stepRunnerRegistry{}
		runner := &promotion.MockStepRunner{}
		registry.register(runner)
		r := registry.getPromotionStepRunner(runner.Name())
		assert.Same(t, runner, r)
	})

	t.Run("registration does not exist", func(t *testing.T) {
		runner := stepRunnerRegistry{}.getPromotionStepRunner("nonexistent")
		assert.Nil(t, runner)
	})
}

func TestStepRunnerRegistry_getHealthChecker(t *testing.T) {
	t.Run("registration exists", func(t *testing.T) {
		registry := stepRunnerRegistry{}
		runner := &health.MockChecker{}
		registry.register(runner)
		r := registry.getHealthChecker(runner.Name())
		assert.Same(t, r, runner)
	})

	t.Run("registration does not exist", func(t *testing.T) {
		runner := stepRunnerRegistry{}.getHealthChecker("nonexistent")
		assert.Nil(t, runner)
	})
}
