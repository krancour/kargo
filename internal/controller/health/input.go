package health

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
)

// Input is an opaque map of values used as input to a Checker.
type Input map[string]any

// DeepCopy returns a deep copy of the input.
func (i Input) DeepCopy() Input {
	if i == nil {
		return nil
	}
	// TODO(hidde): we piggyback on the runtime package for now, as we expect the
	// input to originate from a Kubernetes API object. We should consider writing
	// our own implementation in the future.
	return runtime.DeepCopyJSON(i)
}

// ToJSON marshals the input to JSON.
func (i Input) ToJSON() []byte {
	if len(i) == 0 {
		return nil
	}
	b, _ := json.Marshal(i)
	return b
}

// InputToStruct converts an Input to a (typed) struct.
func InputToStruct[T any](input Input) (T, error) {
	var result T

	// Convert the map to JSON
	jsonData, err := json.Marshal(input)
	if err != nil {
		return result, err
	}

	// Unmarshal the JSON data into the struct
	err = json.Unmarshal(jsonData, &result)
	if err != nil {
		return result, err
	}

	return result, nil
}
