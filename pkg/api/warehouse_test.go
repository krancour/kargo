package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
)

func TestGetWarehouse(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, kargoapi.SchemeBuilder.AddToScheme(scheme))

	testCases := []struct {
		name       string
		client     client.Client
		assertions func(*testing.T, *kargoapi.Warehouse, error)
	}{
		{
			name:   "not found",
			client: fake.NewClientBuilder().WithScheme(scheme).Build(),
			assertions: func(t *testing.T, warehouse *kargoapi.Warehouse, err error) {
				require.NoError(t, err)
				require.Nil(t, warehouse)
			},
		},

		{
			name: "found",
			client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&kargoapi.Warehouse{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "fake-warehouse",
						Namespace: "fake-namespace",
					},
				},
			).Build(),
			assertions: func(t *testing.T, warehouse *kargoapi.Warehouse, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-warehouse", warehouse.Name)
				require.Equal(t, "fake-namespace", warehouse.Namespace)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			warehouse, err := GetWarehouse(
				t.Context(),
				testCase.client,
				types.NamespacedName{
					Namespace: "fake-namespace",
					Name:      "fake-warehouse",
				},
			)
			testCase.assertions(t, warehouse, err)
		})
	}
}

func TestListFreightFromWarehouse(t *testing.T) {
	const testProject = "fake-project"
	const testWarehouse = "fake-warehouse"
	const testStage = "fake-stage"
	const testUpstreamStage = "fake-upstream-stage"
	const testUpstreamStage2 = "fake-upstream-stage2"

	testCases := []struct {
		name       string
		opts       *ListWarehouseFreightOptions
		seed       []*kargoapi.Freight
		hookErr    error
		assertions func(*testing.T, []kargoapi.Freight, error)
	}{
		{
			name:    "error listing Freight",
			hookErr: errors.New("something went wrong"),
			assertions: func(t *testing.T, result []kargoapi.Freight, err error) {
				require.ErrorContains(t, err, "error listing Freight")
				require.ErrorContains(t, err, "something went wrong")
				require.Nil(t, result)
			},
		},
		{
			name: "success with no options",
			seed: []*kargoapi.Freight{
				{ // This should be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
				},
				{ // This should not be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "another-fake-freight",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: "wrong-warehouse",
					},
				},
				{ // This should not be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "wrong-project",
						Name:      "another-fake-freight",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
				},
			},
			assertions: func(t *testing.T, result []kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Len(t, result, 1)
				require.Equal(t, testProject, result[0].Namespace)
				require.Equal(t, "fake-freight", result[0].Name)
			},
		},
		{
			name: "success with VerifiedIn and RequiredSoakTime options",
			opts: &ListWarehouseFreightOptions{
				ApprovedFor:      testStage,
				VerifiedIn:       []string{testUpstreamStage},
				RequiredSoakTime: &metav1.Duration{Duration: time.Hour},
			},
			seed: []*kargoapi.Freight{
				{ // This should not be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-1",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: "wrong-warehouse",
					},
					Status: kargoapi.FreightStatus{
						// Doesn't matter that it's approved for the stage, because this is
						// the wrong warehouse
						ApprovedFor: map[string]kargoapi.ApprovedStage{testStage: {}},
						// Doesn't matter that it's verified upstream, because this is the
						// wrong warehouse
						VerifiedIn: map[string]kargoapi.VerifiedStage{testUpstreamStage: {
							LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
						}},
					},
				},
				{ // This should not be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-2",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					// This is not approved or verified in any Stages
				},
				{ // This should be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-3",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is approved for the Stage
						ApprovedFor: map[string]kargoapi.ApprovedStage{testStage: {}},
					},
				},
				{ // This should not be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-4",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is verified in the upstream Stage, but the soak time has not
						// yet elapsed
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 30 * time.Minute},
							},
						},
					},
				},
				{ // This should be returned
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-5",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is verified in the upstream Stage and the soak time has
						// elapsed
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, result []kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Len(t, result, 2)
				require.Equal(t, testProject, result[0].Namespace)
				require.Equal(t, "fake-freight-3", result[0].Name)
				require.Equal(t, testProject, result[1].Namespace)
				require.Equal(t, "fake-freight-5", result[1].Name)
			},
		},
		{
			name: "success with AvailabilityStrategy set to FreightAvailabilityStrategyAll",
			opts: &ListWarehouseFreightOptions{
				AvailabilityStrategy: kargoapi.FreightAvailabilityStrategyAll,
				ApprovedFor:          testStage,
				VerifiedIn:           []string{testUpstreamStage, testUpstreamStage2},
				RequiredSoakTime:     &metav1.Duration{Duration: time.Hour},
			},
			seed: []*kargoapi.Freight{
				{ // This should be returned as its approved for the Stage
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-1",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is approved for the Stage
						ApprovedFor: map[string]kargoapi.ApprovedStage{testStage: {}},
						// This is only verified in a single upstream Stage
						VerifiedIn: map[string]kargoapi.VerifiedStage{},
					},
				},
				{
					// This should be returned because its verified in both upstream Stages and soak time has lapsed
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-2",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is not approved for any Stage
						ApprovedFor: map[string]kargoapi.ApprovedStage{},
						// This is verified in all of the upstream Stages and the soak time has lapsed
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
							testUpstreamStage2: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
						},
					},
				},
				{ // This should not be returned because it's not verified in all of the upstream Stages
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-3",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is not approved for any Stage
						ApprovedFor: map[string]kargoapi.ApprovedStage{},
						// This is not verified in all of the upstream Stages
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {},
						},
					},
				},
				{ // This should not be returned because its not passed the soak time in all Stages
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-4",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is not approved for any Stage
						ApprovedFor: map[string]kargoapi.ApprovedStage{},
						// This is verified in all of the upstream Stages but only passed the soak time of one
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
							testUpstreamStage2: {
								LongestCompletedSoak: &metav1.Duration{Duration: 30 * time.Minute},
							},
						},
					},
				},
			},
			assertions: func(t *testing.T, result []kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Len(t, result, 2)
				require.Equal(t, testProject, result[0].Namespace)
				require.Equal(t, "fake-freight-1", result[0].Name)
				require.Equal(t, testProject, result[1].Namespace)
				require.Equal(t, "fake-freight-2", result[1].Name)
			},
		},
		{
			name: "success listing Freight currently in upstream Stage",
			opts: &ListWarehouseFreightOptions{
				AvailabilityStrategy: kargoapi.FreightAvailabilityStrategyAll,
				ApprovedFor:          testStage,
				VerifiedIn:           []string{testUpstreamStage},
				RequiredSoakTime:     &metav1.Duration{Duration: time.Hour},
				CurrentlyIn:          testUpstreamStage,
			},
			seed: []*kargoapi.Freight{
				// This would ordinarily be returned because it is approved for the
				// Stage, verified in the upstream Stage, and the soak time has elapsed,
				// BUT this test case asks only for Freight that's currently in the
				// upstream Stage.
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-1",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is approved for the Stage.
						ApprovedFor: map[string]kargoapi.ApprovedStage{testStage: {}},
						// This is verified in the upstream Stage and the soak time has
						// elapsed.
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
						},
					},
				},
				// This should be returned because: Its verified upstream, soak time has
				// elapsed, and it's currently in use by the upstream Stage.
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject,
						Name:      "fake-freight-2",
					},
					Origin: kargoapi.FreightOrigin{
						Kind: kargoapi.FreightOriginKindWarehouse,
						Name: testWarehouse,
					},
					Status: kargoapi.FreightStatus{
						// This is approved for the Stage.
						ApprovedFor: map[string]kargoapi.ApprovedStage{testStage: {}},
						// This is verified in the upstream Stage and the soak time has
						// elapsed.
						VerifiedIn: map[string]kargoapi.VerifiedStage{
							testUpstreamStage: {
								LongestCompletedSoak: &metav1.Duration{Duration: 2 * time.Hour},
							},
						},
						// This Freight is currently in the upstream Stage.
						CurrentlyIn: map[string]kargoapi.CurrentStage{testUpstreamStage: {}},
					},
				},
			},
			assertions: func(t *testing.T, result []kargoapi.Freight, err error) {
				require.NoError(t, err)
				require.Len(t, result, 1)
				require.Equal(t, testProject, result[0].Namespace)
				require.Equal(t, "fake-freight-2", result[0].Name)
			},
		},
		{
			name: "failure with invalid AvailabilityStrategy",
			opts: &ListWarehouseFreightOptions{
				AvailabilityStrategy: "Invalid",
				ApprovedFor:          testStage,
				VerifiedIn:           []string{testUpstreamStage, testUpstreamStage2},
				RequiredSoakTime:     &metav1.Duration{Duration: time.Hour},
			},
			assertions: func(t *testing.T, _ []kargoapi.Freight, err error) {
				require.ErrorContains(t, err, "unsupported AvailabilityStrategy")
			},
		},
	}

	for _, testCase := range testCases {
		store := memory.NewStore(testCase.seed...)
		if testCase.hookErr != nil {
			store.Hooks.List = func(context.Context, string, freight.ListOptions) error {
				return testCase.hookErr
			}
		}

		t.Run(testCase.name, func(t *testing.T) {
			warehouse := &kargoapi.Warehouse{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testProject,
					Name:      "fake-warehouse",
				},
			}
			result, err := ListFreightFromWarehouse(
				t.Context(), store, warehouse, testCase.opts,
			)
			testCase.assertions(t, result, err)
		})
	}
}
