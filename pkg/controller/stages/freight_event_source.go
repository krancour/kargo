package stages

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/event"

	libFreight "github.com/akuity/kargo/pkg/freight"
	"github.com/akuity/kargo/pkg/logging"
)

// freightEventBufferSize is the size of each channel returned by
// FreightEventSource.Channel.
const freightEventBufferSize = 64

// FreightEventSource watches the Freight store for changes and fans each event
// out to every channel obtained from Channel. It holds no per-Freight state.
// It implements manager.Runnable and runs only on the leader (the default for
// runnables that do not implement LeaderElectionRunnable), like the controllers
// whose queues it feeds.
type FreightEventSource struct {
	store libFreight.Store
	// backoff governs the delay before re-opening the watch. Each successful
	// open starts again from this value.
	backoff wait.Backoff
	mu      sync.Mutex
	outputs []chan event.TypedGenericEvent[libFreight.Event]
}

// NewFreightEventSource returns a FreightEventSource that watches the given
// store.
func NewFreightEventSource(store libFreight.Store) *FreightEventSource {
	return &FreightEventSource{
		store: store,
		backoff: wait.Backoff{
			Duration: time.Second,
			Factor:   2,
			Steps:    10,
			Cap:      30 * time.Second,
			Jitter:   0.1,
		},
	}
}

// Channel registers and returns a new output channel. Call it before Start.
func (s *FreightEventSource) Channel() <-chan event.TypedGenericEvent[libFreight.Event] {
	ch := make(chan event.TypedGenericEvent[libFreight.Event], freightEventBufferSize)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs = append(s.outputs, ch)
	return ch
}

// Start implements manager.Runnable. It returns when ctx is done.
func (s *FreightEventSource) Start(ctx context.Context) error {
	logger := logging.LoggerFromContext(ctx)
	s.mu.Lock()
	outputs := s.outputs
	s.mu.Unlock()
	backoff := s.backoff
	for {
		events, err := s.store.Watch(ctx, "")
		if err != nil {
			logger.Error(err, "error watching Freight; retrying")
			if !sleepCtx(ctx, backoff.Step()) {
				return nil
			}
			continue
		}
		backoff = s.backoff
		for ev := range events {
			for _, out := range outputs {
				select {
				case out <- event.TypedGenericEvent[libFreight.Event]{Object: ev}:
				case <-ctx.Done():
					return nil
				}
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		logger.Info("Freight watch closed; reopening")
		// Wait before reopening so that a watch that keeps closing right away
		// cannot spin.
		if !sleepCtx(ctx, backoff.Step()) {
			return nil
		}
	}
}

// sleepCtx waits for d to elapse. It returns false if ctx is done first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
