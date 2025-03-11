package directive

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
)

// Config is an opaque map of configuration values for both PromotionSteps and
// HealthCheckSteps. The keys and values are arbitrary and implementations of
// the Directive interface are responsible for interpreting them.
type Config map[string]any

// DeepCopy returns a deep copy of the configuration.
func (c Config) DeepCopy() Config {
	if c == nil {
		return nil
	}
	// TODO(hidde): we piggyback on the runtime package for now, as we expect
	// the configuration to originate from a Kubernetes API object. We should
	// consider writing our own implementation in the future.
	return runtime.DeepCopyJSON(c)
}

// ToJSON marshals the configuration to JSON.
func (c Config) ToJSON() []byte {
	if len(c) == 0 {
		return nil
	}
	b, _ := json.Marshal(c)
	return b
}
