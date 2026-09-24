package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight/memory"
	"github.com/akuity/kargo/pkg/server/config"
)

func Test_server_queryFreight(t *testing.T) {
	testProject := &kargoapi.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "fake-project"},
	}
	testWarehouse := &kargoapi.Warehouse{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-warehouse",
			Namespace: testProject.Name,
		},
	}
	testFreight1 := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-freight-1",
			Namespace: testProject.Name,
		},
		Origin: kargoapi.FreightOrigin{
			Kind: kargoapi.FreightOriginKindWarehouse,
			Name: testWarehouse.Name,
		},
		Images: []kargoapi.Image{
			{RepoURL: "example.com/image", Tag: "v1.0.0"},
		},
	}
	testFreight2 := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-freight-2",
			Namespace: testProject.Name,
		},
		Origin: kargoapi.FreightOrigin{
			Kind: kargoapi.FreightOriginKindWarehouse,
			Name: testWarehouse.Name,
		},
		Commits: []kargoapi.GitCommit{
			{RepoURL: "https://github.com/example/repo", ID: "abc123"},
		},
	}

	testRESTEndpoint(
		t, &config.ServerConfig{},
		http.MethodGet, "/v1beta1/projects/"+testProject.Name+"/freight",
		[]restTestCase{
			{
				name:          "Project not found",
				clientBuilder: fake.NewClientBuilder(),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "not authorized to list Freight",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore:  memory.NewStore(testFreight1, testFreight2),
				serverSetup: func(_ *testing.T, s *server) {
					s.authorizeFn = denyFreightFn("list", "")
				},
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusForbidden, w.Code)
				},
			},
			{
				name:          "List all freight",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore:  memory.NewStore(testFreight1, testFreight2),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
					var resp queryFreightsResponse
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
					require.Len(t, resp.Groups[""].Items, 2)
					require.NotEmpty(t, resp.ResourceVersion)
				},
			},
			{
				name:          "List freight by origins",
				url:           "/v1beta1/projects/" + testProject.Name + "/freight?origins=" + testWarehouse.Name,
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore: memory.NewStore(
					testFreight1,
					&kargoapi.Freight{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "other-freight",
							Namespace: testProject.Name,
						},
						Origin: kargoapi.FreightOrigin{
							Kind: kargoapi.FreightOriginKindWarehouse,
							Name: "other-warehouse",
						},
					},
				),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
					var resp queryFreightsResponse
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
					items := resp.Groups[""].Items
					require.Len(t, items, 1)
					require.Equal(t, testFreight1.Name, items[0].Name)
					require.NotEmpty(t, resp.ResourceVersion)
				},
			},
			{
				name: "List freight available to a Stage",
				url:  "/v1beta1/projects/" + testProject.Name + "/freight?stage=fake-stage",
				clientBuilder: fake.NewClientBuilder().WithObjects(
					testProject,
					testWarehouse,
					&kargoapi.Stage{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "fake-stage",
							Namespace: testProject.Name,
						},
						Spec: kargoapi.StageSpec{
							RequestedFreight: []kargoapi.FreightRequest{{
								Origin:  testFreight1.Origin,
								Sources: kargoapi.FreightSources{Direct: true},
							}},
						},
					},
				),
				freightStore: memory.NewStore(testFreight1, testFreight2),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
					var resp queryFreightsResponse
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
					require.Len(t, resp.Groups[""].Items, 2)
					require.Empty(t, resp.ResourceVersion)
				},
			},
			{
				name:          "Stage not found when filtering by stage",
				url:           "/v1beta1/projects/" + testProject.Name + "/freight?stage=nonexistent-stage",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "Invalid groupBy value",
				url:           "/v1beta1/projects/" + testProject.Name + "/freight?groupBy=invalid",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
				},
			},
			{
				name:          "Group by image repository",
				url:           "/v1beta1/projects/" + testProject.Name + "/freight?groupBy=image_repo",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore:  memory.NewStore(testFreight1),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
				},
			},
			{
				name:          "Group by git repository",
				url:           "/v1beta1/projects/" + testProject.Name + "/freight?groupBy=git_repo",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				freightStore:  memory.NewStore(testFreight2),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
				},
			},
		},
	)
}

func Test_server_queryFreight_watch(t *testing.T) {
	const projectName = "fake-project"
	// Cases run in parallel and the fake client mutates the objects it is
	// seeded with, so each case gets its own copy of this.
	testProject := &kargoapi.Project{
		ObjectMeta: metav1.ObjectMeta{Name: projectName},
	}
	allStore := memory.NewStore(&kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: projectName,
			Name:      "freight-1",
		},
	})
	filteredStore := memory.NewStore()

	testRESTWatchEndpoint(
		t, &config.ServerConfig{},
		"/v1beta1/projects/"+projectName+"/freight?watch=true",
		[]restWatchTestCase{
			{
				name:          "project not found",
				url:           "/v1beta1/projects/non-existent/freight?watch=true",
				clientBuilder: fake.NewClientBuilder(),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "not authorized to watch Freight",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject.DeepCopy()),
				serverSetup: func(_ *testing.T, s *server) {
					s.authorizeFn = denyFreightFn("watch", "")
				},
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusForbidden, w.Code)
				},
			},
			{
				name:          "watches all freight successfully",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject.DeepCopy()),
				freightStore:  allStore,
				operations: func(ctx context.Context, _ client.Client) {
					// Create a new piece of freight to trigger a watch event
					_ = allStore.Create(ctx, &kargoapi.Freight{
						ObjectMeta: metav1.ObjectMeta{Namespace: projectName},
						Images: []kargoapi.Image{
							{RepoURL: "example.com/image", Tag: "v2.0.0"},
						},
					})
				},
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)

					// Verify SSE headers
					require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
					require.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
					require.Equal(t, "keep-alive", w.Header().Get("Connection"))

					// The response body should contain SSE events from the create operation
					body := w.Body.String()
					require.Contains(t, body, "data:")
					require.Contains(t, body, `"type":"ADDED"`)
					require.Contains(t, body, "v2.0.0")
					// Pre-existing Freight is not replayed
					require.NotContains(t, body, `"freight-1"`)
				},
			},
			{
				name:          "filters watch events by origins",
				url:           "/v1beta1/projects/" + projectName + "/freight?watch=true&origins=warehouse-1",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject.DeepCopy()),
				freightStore:  filteredStore,
				operations: func(ctx context.Context, _ client.Client) {
					// Freight from the requested warehouse (should be sent)
					_ = filteredStore.Create(ctx, &kargoapi.Freight{
						ObjectMeta: metav1.ObjectMeta{Namespace: projectName},
						Origin: kargoapi.FreightOrigin{
							Kind: kargoapi.FreightOriginKindWarehouse,
							Name: "warehouse-1",
						},
					})
					// Freight from a different warehouse (should be filtered out)
					_ = filteredStore.Create(ctx, &kargoapi.Freight{
						ObjectMeta: metav1.ObjectMeta{Namespace: projectName},
						Origin: kargoapi.FreightOrigin{
							Kind: kargoapi.FreightOriginKindWarehouse,
							Name: "warehouse-2",
						},
					})
				},
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)
					require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))

					body := w.Body.String()
					require.Contains(t, body, `"type":"ADDED"`)
					require.Contains(t, body, "warehouse-1")
					require.NotContains(t, body, "warehouse-2")
				},
			},
			{
				name:          "watches empty freight list",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject.DeepCopy()),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusOK, w.Code)

					// Verify SSE headers are set
					require.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
				},
			},
		},
	)
}
