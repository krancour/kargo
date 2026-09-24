package freight

import (
	"slices"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// Changes describes which Stage names were added to each of a Freight's
// status maps by a single write. It lets consumers react to a change without
// holding the previous state themselves.
type Changes struct {
	NewlyVerifiedIn  []string `json:"newlyVerifiedIn,omitempty"`
	NewlyCurrentlyIn []string `json:"newlyCurrentlyIn,omitempty"`
	NewlyApprovedFor []string `json:"newlyApprovedFor,omitempty"`
}

// IsEmpty returns true when no keys were added to any map.
func (c Changes) IsEmpty() bool {
	return len(c.NewlyVerifiedIn) == 0 &&
		len(c.NewlyCurrentlyIn) == 0 &&
		len(c.NewlyApprovedFor) == 0
}

// ComputeChanges returns the Stage names present in updated's maps but absent
// from old's. Each slice is sorted; a slice with no additions is nil.
func ComputeChanges(old, updated kargoapi.FreightStatus) Changes {
	return Changes{
		NewlyVerifiedIn:  newKeys(old.VerifiedIn, updated.VerifiedIn),
		NewlyCurrentlyIn: newKeys(old.CurrentlyIn, updated.CurrentlyIn),
		NewlyApprovedFor: newKeys(old.ApprovedFor, updated.ApprovedFor),
	}
}

// newKeys returns the sorted keys present in updated but absent from old, or
// nil if there are none.
func newKeys[V any](old, updated map[string]V) []string {
	var keys []string
	for k := range updated {
		if _, ok := old[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}
