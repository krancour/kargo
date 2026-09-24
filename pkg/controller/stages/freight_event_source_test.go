package stages

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/event"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libFreight "github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/freight/memory"
)

const freightEventTimeout = 5 * time.Second

// testWatchStore wraps a memory Store. It can replace the result of chosen
// Watch calls and signals opened each time a Watch call reaches the memory
// Store, so tests can write Freight only once the watch is open.
type testWatchStore struct {
	*memory.Store
	calls atomic.Int32
	// overrides maps a 1-based Watch call number to the behavior of that call.
	// A call with no entry is passed through to the memory Store.
	overrides map[int32]func() (<-chan libFreight.Event, error)
	opened    chan struct{}
}

func newTestWatchStore(
	overrides map[int32]func() (<-chan libFreight.Event, error),
) *testWatchStore {
	return &testWatchStore{
		Store:     memory.NewStore(),
		overrides: overrides,
		opened:    make(chan struct{}, 1),
	}
}

func (s *testWatchStore) Watch(
	ctx context.Context,
	project string,
) (<-chan libFreight.Event, error) {
	if fn, ok := s.overrides[s.calls.Add(1)]; ok {
		return fn()
	}
	ch, err := s.Store.Watch(ctx, project)
	s.opened <- struct{}{}
	return ch, err
}

func closedWatch() (<-chan libFreight.Event, error) {
	ch := make(chan libFreight.Event)
	close(ch)
	return ch, nil
}

func erroringWatch() (<-chan libFreight.Event, error) {
	return nil, errors.New("something went wrong")
}

// startFreightEventSource starts a FreightEventSource over store with the
// given number of output channels, waits for its watch to open, and returns
// the channels, a function that stops the source, and a channel that
// receives the error Start returns.
func startFreightEventSource(
	t *testing.T,
	store *testWatchStore,
	numChannels int,
) ([]<-chan event.TypedGenericEvent[libFreight.Event], context.CancelFunc, <-chan error) {
	t.Helper()
	s := NewFreightEventSource(store)
	// Keep retries fast.
	s.backoff = wait.Backoff{Duration: time.Millisecond, Factor: 1, Steps: 1}
	chs := make([]<-chan event.TypedGenericEvent[libFreight.Event], numChannels)
	for i := range chs {
		chs[i] = s.Channel()
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start(ctx) }()
	select {
	case <-store.opened:
	case <-time.After(freightEventTimeout):
		require.FailNow(t, "timed out waiting for the Freight watch to open")
	}
	return chs, cancel, errCh
}

func createTestFreight(t *testing.T, store libFreight.Store) string {
	t.Helper()
	f := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fake-project"},
		Origin: kargoapi.FreightOrigin{
			Kind: kargoapi.FreightOriginKindWarehouse,
			Name: "fake-warehouse",
		},
		Commits: []kargoapi.GitCommit{{
			RepoURL: "https://example.com/repo.git",
			ID:      "fake-commit",
		}},
	}
	require.NoError(t, store.Create(t.Context(), f))
	return f.Name
}

func requireAdded(
	t *testing.T,
	name string,
	chs ...<-chan event.TypedGenericEvent[libFreight.Event],
) {
	t.Helper()
	for _, ch := range chs {
		evt := receive(t, ch)
		require.Equal(t, watch.Added, evt.Object.Type)
		require.Equal(t, name, evt.Object.Object.Name)
	}
}

func receive(
	t *testing.T,
	ch <-chan event.TypedGenericEvent[libFreight.Event],
) event.TypedGenericEvent[libFreight.Event] {
	t.Helper()
	select {
	case evt := <-ch:
		return evt
	case <-time.After(freightEventTimeout):
		require.FailNow(t, "timed out waiting for Freight event")
	}
	return event.TypedGenericEvent[libFreight.Event]{}
}

func TestFreightEventSource(t *testing.T) {
	t.Run("fans Added events out to every channel", func(t *testing.T) {
		t.Parallel()
		store := newTestWatchStore(nil)
		chs, _, _ := startFreightEventSource(t, store, 2)
		requireAdded(t, createTestFreight(t, store), chs...)
	})

	t.Run("fans Modified events with Changes out to every channel", func(t *testing.T) {
		t.Parallel()
		store := newTestWatchStore(nil)
		chs, _, _ := startFreightEventSource(t, store, 2)
		name := createTestFreight(t, store)
		requireAdded(t, name, chs...)
		_, err := store.UpdateStatus(
			t.Context(),
			"fake-project",
			name,
			func(status *kargoapi.FreightStatus) error {
				status.AddVerifiedStage("fake-stage", time.Now())
				return nil
			},
		)
		require.NoError(t, err)
		for _, ch := range chs {
			evt := receive(t, ch)
			require.Equal(t, watch.Modified, evt.Object.Type)
			require.Equal(t, name, evt.Object.Object.Name)
			require.Equal(
				t,
				libFreight.Changes{NewlyVerifiedIn: []string{"fake-stage"}},
				evt.Object.Changes,
			)
		}
	})

	t.Run("returns nil when the context is canceled", func(t *testing.T) {
		t.Parallel()
		_, cancel, errCh := startFreightEventSource(t, newTestWatchStore(nil), 1)
		cancel()
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(freightEventTimeout):
			require.FailNow(t, "Start did not return after cancellation")
		}
	})

	t.Run("reopens the watch when its channel closes", func(t *testing.T) {
		t.Parallel()
		store := newTestWatchStore(
			map[int32]func() (<-chan libFreight.Event, error){1: closedWatch},
		)
		chs, _, _ := startFreightEventSource(t, store, 1)
		requireAdded(t, createTestFreight(t, store), chs...)
		require.Equal(t, int32(2), store.calls.Load())
	})

	t.Run("retries when Watch returns an error", func(t *testing.T) {
		t.Parallel()
		store := newTestWatchStore(
			map[int32]func() (<-chan libFreight.Event, error){1: erroringWatch},
		)
		chs, _, _ := startFreightEventSource(t, store, 1)
		requireAdded(t, createTestFreight(t, store), chs...)
		require.Equal(t, int32(2), store.calls.Load())
	})
}
