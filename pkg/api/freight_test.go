package api

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
)

// TestGenerateFreightID is a smoke test confirming that GenerateFreightID
// delegates to freight.GenerateID.
func TestGenerateFreightID(t *testing.T) {
	f := &kargoapi.Freight{
		Origin: kargoapi.FreightOrigin{
			Kind: "fake-kind",
			Name: "fake-name",
		},
		Commits: []kargoapi.GitCommit{
			{
				RepoURL: "fake-git-repo",
				ID:      "fake-commit-id",
			},
		},
	}
	require.Equal(t, freight.GenerateID(f), GenerateFreightID(f))
}

func TestGetFreight(t *testing.T) {
	testCases := []struct {
		name       string
		store      *memory.Store
		assertions func(*testing.T, *kargoapi.Freight, error)
	}{
		{
			name: "error",
			store: func() *memory.Store {
				s := memory.NewStore()
				s.Hooks.Get = func(context.Context, string, string) error {
					return errors.New("something went wrong")
				}
				return s
			}(),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.ErrorContains(t, err, "something went wrong")
				require.Nil(t, f)
			},
		},
		{
			name:  "not found",
			store: memory.NewStore(),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Nil(t, f)
			},
		},
		{
			name: "found",
			store: memory.NewStore(
				&kargoapi.Freight{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "fake-freight",
						Namespace: "fake-namespace",
					},
				},
			),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-freight", f.Name)
				require.Equal(t, "fake-namespace", f.Namespace)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f, err := GetFreight(
				t.Context(),
				testCase.store,
				types.NamespacedName{
					Namespace: "fake-namespace",
					Name:      "fake-freight",
				},
			)
			testCase.assertions(t, f, err)
		})
	}
}

func TestGetFreightByAlias(t *testing.T) {
	testCases := []struct {
		name       string
		store      *memory.Store
		assertions func(*testing.T, *kargoapi.Freight, error)
	}{
		{
			name: "error",
			store: func() *memory.Store {
				s := memory.NewStore()
				s.Hooks.GetByAlias = func(context.Context, string, string) error {
					return errors.New("something went wrong")
				}
				return s
			}(),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.ErrorContains(t, err, "something went wrong")
				require.Nil(t, f)
			},
		},
		{
			name:  "not found",
			store: memory.NewStore(),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Nil(t, f)
			},
		},
		{
			name: "found",
			store: memory.NewStore(
				&kargoapi.Freight{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "fake-freight",
						Namespace: "fake-namespace",
						Labels: map[string]string{
							kargoapi.LabelKeyAlias: "fake-alias",
						},
					},
					Alias: "fake-alias",
				},
			),
			assertions: func(t *testing.T, f *kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-freight", f.Name)
				require.Equal(t, "fake-namespace", f.Namespace)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f, err := GetFreightByAlias(
				t.Context(),
				testCase.store,
				"fake-namespace",
				"fake-alias",
			)
			testCase.assertions(t, f, err)
		})
	}
}

func TestListFreightByCurrentStage(t *testing.T) {
	const testProject = "fake-project"
	testStage := &kargoapi.Stage{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testProject,
			Name:      "fake-stage",
		},
	}

	testCases := []struct {
		name       string
		store      *memory.Store
		assertions func(*testing.T, []kargoapi.Freight, error)
	}{
		{
			name: "error listing Freight",
			store: func() *memory.Store {
				s := memory.NewStore()
				s.Hooks.List = func(context.Context, string, freight.ListOptions) error {
					return errors.New("something went wrong")
				}
				return s
			}(),
			assertions: func(t *testing.T, fs []kargoapi.Freight, err error) {
				require.ErrorContains(t, err, "error listing Freight")
				require.ErrorContains(t, err, "something went wrong")
				require.Nil(t, fs)
			},
		},
		{
			name: "success",
			store: memory.NewStore(
				&kargoapi.Freight{ // This should be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-1",
					},
					Status: kargoapi.FreightStatus{
						CurrentlyIn: map[string]kargoapi.CurrentStage{testStage.Name: {}},
					},
				},
				&kargoapi.Freight{ // This should NOT be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-2",
					},
					Status: kargoapi.FreightStatus{
						CurrentlyIn: map[string]kargoapi.CurrentStage{"wrong-stage": {}},
					},
				},
			),
			assertions: func(t *testing.T, fs []kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Len(t, fs, 1)
				require.Equal(t, testProject, fs[0].Namespace)
				require.Equal(t, "fake-freight-1", fs[0].Name)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fs, err := ListFreightByCurrentStage(t.Context(), testCase.store, testStage)
			testCase.assertions(t, fs, err)
		})
	}
}
