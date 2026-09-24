package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/event"
	k8sevent "github.com/akuity/kargo/pkg/event/kubernetes"
	libFreight "github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
	fakeevent "github.com/akuity/kargo/pkg/kubernetes/event/fake"
)

type errSender struct{ err error }

func (s *errSender) Send(_ context.Context, _ event.Meta) error { return s.err }
func (s *errSender) Shutdown()                                  {}

func Test_server_createFreight(t *testing.T) {
	const testImageRepo = "example.com/fake-image"
	testProject := &kargoapi.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "fake-project"},
	}
	testWarehouse := &kargoapi.Warehouse{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testProject.Name,
			Name:      "fake-warehouse",
		},
		Spec: kargoapi.WarehouseSpec{
			InternalSubscriptions: []kargoapi.RepoSubscription{{
				Image: &kargoapi.ImageSubscription{RepoURL: testImageRepo},
			}},
		},
	}
	testOrigin := kargoapi.FreightOrigin{
		Kind: kargoapi.FreightOriginKindWarehouse,
		Name: testWarehouse.Name,
	}
	testImages := []kargoapi.Image{{
		RepoURL: testImageRepo,
		Tag:     "v1.0.0",
		Digest:  "sha256:abc",
	}}
	testFreight := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: testProject.Name},
		Origin:     testOrigin,
		Images:     testImages,
	}
	testFreight.Name = libFreight.GenerateID(testFreight)

	// recorder is reassigned by serverSetup before assertions reads it. Test
	// cases are run sequentially, so this is safe.
	var recorder *fakeevent.EventRecorder
	// store is reassigned per case that needs to inspect it afterward.
	var store *memory.Store

	testRESTEndpoint(
		t, nil,
		http.MethodPost, "/v1beta1/projects/"+testProject.Name+"/freight",
		[]restTestCase{
			{
				name: "project not found",
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusNotFound, w.Code)
				},
			},
			{
				name:          "body is not JSON",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				body:          strings.NewReader("{not json"),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
				},
			},
			{
				name:          "origin kind is not Warehouse",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				body: mustJSONBody(&kargoapi.Freight{
					Origin: kargoapi.FreightOrigin{Kind: "Bogus", Name: "fake-warehouse"},
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), "origin must name a Warehouse")
				},
			},
			{
				name:          "origin name is empty",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				body:          mustJSONBody(&kargoapi.Freight{Images: testImages}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), "origin must name a Warehouse")
				},
			},
			{
				name:          "no artifacts",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				body:          mustJSONBody(&kargoapi.Freight{Origin: testOrigin}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(
						t,
						w.Body.String(),
						"at least one commit, image, chart, or artifact",
					)
				},
			},
			{
				name:          "not authorized to create Freight",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				serverSetup: func(_ *testing.T, s *server) {
					s.authorizeFn = denyFreightFn("create", "")
				},
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusForbidden, w.Code)
				},
			},
			{
				name:          "Warehouse does not exist",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject),
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), "warehouse")
					require.Contains(t, w.Body.String(), "does not exist")
				},
			},
			{
				name:          "artifact not covered by a subscription",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: []kargoapi.Image{{
						RepoURL: "example.com/other-image",
						Tag:     "v1.0.0",
					}},
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), "no subscription found")
				},
			},
			{
				name:          "Freight already exists",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				freightStore:  memory.NewStore(testFreight),
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusConflict, w.Code)
					require.Contains(t, w.Body.String(), "already exists")
				},
			},
			{
				name:          "caller-supplied alias already in use",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				freightStore: memory.NewStore(&kargoapi.Freight{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: testProject.Name,
						Name:      "other-freight",
						Labels:    map[string]string{kargoapi.LabelKeyAlias: "taken"},
					},
					Alias: "taken",
				}),
				body: mustJSONBody(&kargoapi.Freight{
					Alias:  "taken",
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusConflict, w.Code)
					require.Contains(t, w.Body.String(), "alias")
				},
			},
			{
				name:          "event send failure does not fail the request",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				serverSetup: func(_ *testing.T, s *server) {
					s.sender = &errSender{err: errors.New("send failed")}
				},
				body: mustJSONBody(&kargoapi.Freight{
					Origin: testOrigin,
					Images: testImages,
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusCreated, w.Code)
				},
			},
			{
				name:          "success",
				clientBuilder: fake.NewClientBuilder().WithObjects(testProject, testWarehouse),
				serverSetup: func(_ *testing.T, s *server) {
					store = memory.NewStore()
					s.freightStore = store
					recorder = fakeevent.NewEventRecorder(1)
					s.sender = k8sevent.NewEventSender(recorder)
				},
				body: mustJSONBody(&kargoapi.Freight{
					ObjectMeta: metav1.ObjectMeta{Name: "ignored-name"},
					Origin:     kargoapi.FreightOrigin{Name: testWarehouse.Name},
					Images:     testImages,
					Status: kargoapi.FreightStatus{
						CurrentlyIn: map[string]kargoapi.CurrentStage{"fake-stage": {}},
					},
				}),
				assertions: func(t *testing.T, w *httptest.ResponseRecorder, _ client.Client) {
					require.Equal(t, http.StatusCreated, w.Code)
					var got kargoapi.Freight
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
					require.Equal(t, testFreight.Name, got.Name)
					require.Equal(t, testProject.Name, got.Namespace)
					require.NotEmpty(t, got.Alias)
					require.Equal(t, got.Alias, got.Labels[kargoapi.LabelKeyAlias])
					require.Empty(t, got.Status)

					stored, err := store.Get(context.Background(), testProject.Name, got.Name)
					require.NoError(t, err)
					require.Equal(t, got.Alias, stored.Alias)

					require.Len(t, recorder.Events, 1)
					evt := <-recorder.Events
					require.Equal(t, corev1.EventTypeNormal, evt.EventType)
					require.Equal(t, string(kargoapi.EventTypeFreightCreated), evt.Reason)
				},
			},
		},
	)
}
