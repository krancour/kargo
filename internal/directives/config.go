package directives

import (
	"encoding/json"

	"github.com/akuity/kargo/pkg/x/directive"
)

// ConfigToStruct converts a directive.Config to a (typed) configuration struct.
func ConfigToStruct[T any](c directive.Config) (T, error) {
	var result T

	// Convert the map to JSON
	jsonData, err := json.Marshal(c)
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
