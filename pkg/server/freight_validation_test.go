package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/validation/field"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func Test_validateFreightArtifacts(t *testing.T) {
	testCases := []struct {
		name       string
		freight    *kargoapi.Freight
		warehouse  *kargoapi.Warehouse
		assertions func(*testing.T, field.ErrorList)
	}{
		{
			name: "Freight missing artifact",
			freight: &kargoapi.Freight{
				Commits: []kargoapi.GitCommit{},
			},
			warehouse: &kargoapi.Warehouse{
				Spec: kargoapi.WarehouseSpec{
					InternalSubscriptions: []kargoapi.RepoSubscription{
						{
							Git: &kargoapi.GitSubscription{
								RepoURL: "fake-repo-url",
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(t, errs[0], "no artifact found for subscription")
			},
		},
		{
			name: "Freight with duplicate Git artifact for Warehouse subscription",
			freight: &kargoapi.Freight{
				Commits: []kargoapi.GitCommit{
					{
						RepoURL: "fake-repo-url",
					},
					{
						RepoURL: "fake-repo-url",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{
				Spec: kargoapi.WarehouseSpec{
					InternalSubscriptions: []kargoapi.RepoSubscription{
						{
							Git: &kargoapi.GitSubscription{
								RepoURL: "fake-repo-url",
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"multiple artifacts found for subscription",
				)
			},
		},
		{
			name: "Freight with duplicate image artifact for Warehouse subscription",
			freight: &kargoapi.Freight{
				Images: []kargoapi.Image{
					{
						RepoURL: "fake-repo-url",
					},
					{
						RepoURL: "fake-repo-url",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{
				Spec: kargoapi.WarehouseSpec{
					InternalSubscriptions: []kargoapi.RepoSubscription{
						{
							Image: &kargoapi.ImageSubscription{
								RepoURL: "fake-repo-url",
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"multiple artifacts found for subscription",
				)
			},
		},
		{
			name: "Freight with multiple chart artifacts for Warehouse subscription",
			freight: &kargoapi.Freight{
				Charts: []kargoapi.Chart{
					{
						RepoURL: "fake-repo-url",
						Name:    "fake-name",
					},
					{
						RepoURL: "fake-repo-url",
						Name:    "fake-name",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{
				Spec: kargoapi.WarehouseSpec{
					InternalSubscriptions: []kargoapi.RepoSubscription{
						{
							Chart: &kargoapi.ChartSubscription{
								RepoURL: "fake-repo-url",
								Name:    "fake-name",
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"multiple artifacts found for subscription",
				)
			},
		},
		{
			name: "Freight with Git commit not matching Warehouse subscription",
			freight: &kargoapi.Freight{
				Commits: []kargoapi.GitCommit{
					{
						RepoURL: "fake-repo-url",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"no subscription found for Git repository in Warehouse",
				)
			},
		},
		{
			name: "Freight with image repository not matching Warehouse subscription",
			freight: &kargoapi.Freight{
				Images: []kargoapi.Image{
					{
						RepoURL: "fake-repo-url",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"no subscription found for image repository in Warehouse",
				)
			},
		},
		{
			name: "Freight with Helm chart not matching Warehouse subscription",
			freight: &kargoapi.Freight{
				Charts: []kargoapi.Chart{
					{
						RepoURL: "fake-repo-url",
						Name:    "fake-name",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Len(t, errs, 1)
				require.ErrorContains(
					t,
					errs[0],
					"no subscription found for Helm chart in Warehouse",
				)
			},
		},
		{
			name: "success",
			freight: &kargoapi.Freight{
				Commits: []kargoapi.GitCommit{
					{
						RepoURL: "fake-git-repo-url",
					},
					{
						RepoURL: "fake-another-git-repo-url",
					},
				},
				Images: []kargoapi.Image{
					{
						RepoURL: "fake-image-repo-url",
					},
					{
						RepoURL: "fake-another-image-repo-url",
					},
				},
				Charts: []kargoapi.Chart{
					{
						RepoURL: "fake-chart-repo-url",
						Name:    "fake-chart-name",
					},
					{
						RepoURL: "fake-chart-repo-url",
						Name:    "fake-another-chart-name",
					},
					{
						RepoURL: "fake-another-chart-repo-url",
					},
				},
			},
			warehouse: &kargoapi.Warehouse{
				Spec: kargoapi.WarehouseSpec{
					InternalSubscriptions: []kargoapi.RepoSubscription{
						{
							Git: &kargoapi.GitSubscription{
								RepoURL: "fake-git-repo-url",
							},
						},
						{
							Git: &kargoapi.GitSubscription{
								RepoURL: "fake-another-git-repo-url",
							},
						},
						{
							Image: &kargoapi.ImageSubscription{
								RepoURL: "fake-image-repo-url",
							},
						},
						{
							Image: &kargoapi.ImageSubscription{
								RepoURL: "fake-another-image-repo-url",
							},
						},
						{
							Chart: &kargoapi.ChartSubscription{
								RepoURL: "fake-chart-repo-url",
								Name:    "fake-chart-name",
							},
						},
						{
							Chart: &kargoapi.ChartSubscription{
								RepoURL: "fake-chart-repo-url",
								Name:    "fake-another-chart-name",
							},
						},
						{
							Chart: &kargoapi.ChartSubscription{
								RepoURL: "fake-another-chart-repo-url",
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, errs field.ErrorList) {
				require.Empty(t, errs)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			errs := validateFreightArtifacts(testCase.freight, testCase.warehouse)
			testCase.assertions(t, errs)
		})
	}
}
