package freight

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func TestChanges_IsEmpty(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		c    Changes
		want bool
	}{
		{name: "zero value", c: Changes{}, want: true},
		{
			name: "verified in populated",
			c:    Changes{NewlyVerifiedIn: []string{"s1"}},
			want: false,
		},
		{
			name: "currently in populated",
			c:    Changes{NewlyCurrentlyIn: []string{"s1"}},
			want: false,
		},
		{
			name: "approved for populated",
			c:    Changes{NewlyApprovedFor: []string{"s1"}},
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.c.IsEmpty())
		})
	}
}

func TestComputeChanges(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		old  kargoapi.FreightStatus
		new  kargoapi.FreightStatus
		want Changes
	}{
		{
			name: "no change",
			old: kargoapi.FreightStatus{
				VerifiedIn:  map[string]kargoapi.VerifiedStage{"s1": {}},
				CurrentlyIn: map[string]kargoapi.CurrentStage{"s1": {}},
				ApprovedFor: map[string]kargoapi.ApprovedStage{"s1": {}},
			},
			new: kargoapi.FreightStatus{
				VerifiedIn:  map[string]kargoapi.VerifiedStage{"s1": {}},
				CurrentlyIn: map[string]kargoapi.CurrentStage{"s1": {}},
				ApprovedFor: map[string]kargoapi.ApprovedStage{"s1": {}},
			},
			want: Changes{},
		},
		{
			name: "one key added to each map",
			old: kargoapi.FreightStatus{
				VerifiedIn:  map[string]kargoapi.VerifiedStage{},
				CurrentlyIn: map[string]kargoapi.CurrentStage{},
				ApprovedFor: map[string]kargoapi.ApprovedStage{},
			},
			new: kargoapi.FreightStatus{
				VerifiedIn:  map[string]kargoapi.VerifiedStage{"s1": {}},
				CurrentlyIn: map[string]kargoapi.CurrentStage{"s2": {}},
				ApprovedFor: map[string]kargoapi.ApprovedStage{"s3": {}},
			},
			want: Changes{
				NewlyVerifiedIn:  []string{"s1"},
				NewlyCurrentlyIn: []string{"s2"},
				NewlyApprovedFor: []string{"s3"},
			},
		},
		{
			name: "keys removed are not reported",
			old: kargoapi.FreightStatus{
				VerifiedIn: map[string]kargoapi.VerifiedStage{"s1": {}, "s2": {}},
			},
			new: kargoapi.FreightStatus{
				VerifiedIn: map[string]kargoapi.VerifiedStage{"s1": {}},
			},
			want: Changes{},
		},
		{
			name: "two keys added are sorted",
			old:  kargoapi.FreightStatus{},
			new: kargoapi.FreightStatus{
				VerifiedIn: map[string]kargoapi.VerifiedStage{"s2": {}, "s1": {}},
			},
			want: Changes{NewlyVerifiedIn: []string{"s1", "s2"}},
		},
		{
			name: "old nil maps report all new keys",
			old:  kargoapi.FreightStatus{},
			new: kargoapi.FreightStatus{
				VerifiedIn:  map[string]kargoapi.VerifiedStage{"s1": {}},
				CurrentlyIn: map[string]kargoapi.CurrentStage{"s2": {}},
				ApprovedFor: map[string]kargoapi.ApprovedStage{"s3": {}},
			},
			want: Changes{
				NewlyVerifiedIn:  []string{"s1"},
				NewlyCurrentlyIn: []string{"s2"},
				NewlyApprovedFor: []string{"s3"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ComputeChanges(tc.old, tc.new)
			require.Equal(t, tc.want.NewlyVerifiedIn, got.NewlyVerifiedIn)
			require.Equal(t, tc.want.NewlyCurrentlyIn, got.NewlyCurrentlyIn)
			require.Equal(t, tc.want.NewlyApprovedFor, got.NewlyApprovedFor)
			require.Equal(t, tc.want.IsEmpty(), got.IsEmpty())
		})
	}
}

func TestChanges_JSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Changes{
		NewlyVerifiedIn:  []string{"a"},
		NewlyCurrentlyIn: []string{"b"},
		NewlyApprovedFor: []string{"c"},
	})
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"newlyVerifiedIn":["a"],"newlyCurrentlyIn":["b"],"newlyApprovedFor":["c"]}`,
		string(data),
	)

	data, err = json.Marshal(Changes{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))
}
