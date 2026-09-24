package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

// watchBufferSize is the size of the buffered channel returned by Watch.
// Sends block until there is room in the buffer or ctx is done -- a slow
// consumer applies backpressure rather than dropping events.
const watchBufferSize = 64

// notifyPayload is the JSON payload sent via pg_notify by the freight_notify
// trigger.
type notifyPayload struct {
	Op      string `json:"op"`
	Project string `json:"project"`
	Name    string `json:"name"`
}

// Watch implements freight.Store. It opens a dedicated connection, outside
// the pool, for the lifetime of the watch -- one connection per watch is an
// accepted cost of this implementation. Keeping it out of the pool means open
// watches never starve other callers of pooled connections, and a LISTENing
// connection can never be handed to an unrelated caller.
func (s *store) Watch(ctx context.Context, project string) (<-chan freight.Event, error) {
	// The pool's ConnConfig is used rather than the raw URL because pgx.Connect
	// rejects pgxpool-only URL parameters such as pool_max_conns.
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig)
	if err != nil {
		return nil, fmt.Errorf("error opening connection for watch: %w", err)
	}
	if _, err = conn.Exec(ctx, "LISTEN freight"); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("error listening for freight notifications: %w", err)
	}

	ch := make(chan freight.Event, watchBufferSize)

	go func() {
		defer func() {
			_ = conn.Close(context.Background())
		}()
		defer close(ch)
		for {
			n, err := conn.WaitForNotification(ctx)
			if err != nil {
				// ctx was canceled, or the connection is no longer usable.
				return
			}

			var p notifyPayload
			if json.Unmarshal([]byte(n.Payload), &p) != nil || p.Project != project {
				continue
			}

			var ev freight.Event
			switch p.Op {
			case "DELETE":
				ev = freight.Event{
					Type: watch.Deleted,
					Object: &kargoapi.Freight{
						TypeMeta:   freightTypeMeta,
						ObjectMeta: metav1.ObjectMeta{Namespace: p.Project, Name: p.Name},
					},
				}
			default: // INSERT, UPDATE
				f, err := s.Get(ctx, p.Project, p.Name)
				if err != nil {
					// Any error here (not found because it was deleted meanwhile,
					// a transient connection error, etc.) drops this event. In the
					// deleted case, a corresponding DELETE notification will follow
					// (or already has).
					continue
				}
				t := watch.Modified
				if p.Op == "INSERT" {
					t = watch.Added
				}
				ev = freight.Event{Type: t, Object: f}
			}

			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}
