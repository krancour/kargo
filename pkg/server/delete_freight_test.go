package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libFreight "github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
	"github.com/akuity/kargo/pkg/indexer"
	"github.com/akuity/kargo/pkg/server/config"
)

func Test_server_deleteFreight(t *testing.T) {
	testProject := &kargoapi.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "fake-project"},
	}
	testFreight := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testProject.Name,
			Name:      "fake-freight",
			Labels: map[string]string{
				kargoapi.LabelKeyAlias: "fake-alias",
			},
		},
		Alias: "fake-alias",
	}
	testStage := &kargoapi.Stage{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testProject.Name,
			Name:      "fake-stage",
		},
		Status: kargoapi.StageStatus{
			FreightHistory: kargoapi.FreightHistory{{
				Freight: map[string]kargoapi.FreightReference{
					"fake-warehouse": {Name: testFreight.Name},
				},
			}},
		},
	}
	deletedByNameStore := memory.NewStore(testFreight)
	deletedByAliasStore := memory.NewStore(testFreight)
	inUseStore := memory.NewStore(testFreight)
	testRESTEndpoint(
		t, &config.ServerConfig{},
		http.MethodDelete, "/v1beta1/projects/"+testProject.Name+"/freight/"+testFreight.Name,
		[]restTestCase{
			{
				name: "Project does not exist",
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "Freight does not exist",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "not authorized to delete Freight",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore:  memory.NewStore(testFreight),
				serverSetup: func(_ *testing.T, s *server) {
					s.authorizeFn = denyFreightFn("delete", "")
				},
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusForbidden, w.Code)
				},
			},
			{
				name: "Freight in use by a Stage",
				clientBuilder: fake.NewClientBuilder().
					// The Stage index can only be registered once the scheme knows
					// about Stages.
					WithScheme(newTestScheme(t)).
					WithObjects(testProject, testStage).
					WithIndex(
						&kargoapi.Stage{},
						indexer.StagesByFreightField,
						indexer.StagesByFreight,
					),
				freightStore: inUseStore,
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusConflict, w.Code)
					require.Contains(t, w.Body.String(), testStage.Name)

					// Verify the Freight was NOT deleted
					_, err := inUseStore.Get(t.Context(), testProject.Name, testFreight.Name)
					require.NoError(t, err)
				},
			},
			{
				name: "deletes Freight by name",
				clientBuilder: fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithObjects(testProject).
					WithIndex(
						&kargoapi.Stage{},
						indexer.StagesByFreightField,
						indexer.StagesByFreight,
					),
				freightStore: deletedByNameStore,
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNoContent, w.Code)

					// Verify the Freight was deleted from the store
					_, err := deletedByNameStore.Get(t.Context(), testProject.Name, testFreight.Name)
					require.ErrorIs(t, err, libFreight.ErrNotFound)
				},
			},
			{
				name: "deletes Freight by alias",
				url:  "/v1beta1/projects/" + testProject.Name + "/freight/" + testFreight.Alias,
				clientBuilder: fake.NewClientBuilder().
					WithScheme(newTestScheme(t)).
					WithObjects(testProject).
					WithIndex(
						&kargoapi.Stage{},
						indexer.StagesByFreightField,
						indexer.StagesByFreight,
					),
				freightStore: deletedByAliasStore,
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNoContent, w.Code)

					// Verify the Freight was deleted from the store
					_, err := deletedByAliasStore.Get(t.Context(), testProject.Name, testFreight.Name)
					require.ErrorIs(t, err, libFreight.ErrNotFound)
				},
			},
		},
	)
}
