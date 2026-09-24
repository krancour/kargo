package postgres

import (
	"context"
	neturl "net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

// newTestStore returns a Store backed by TEST_DATABASE_URL, truncating the
// freight table first so that test cases start from a clean slate. Tests
// using it are not run in parallel with one another because they share the
// table.
func newTestStore(t *testing.T) freight.Store {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	s, err := NewStore(t.Context(), Config{URL: url})
	require.NoError(t, err)
	st, ok := s.(*store)
	require.True(t, ok)
	_, err = st.pool.Exec(t.Context(), "TRUNCATE freight")
	require.NoError(t, err)
	return s
}

func newFreight(project, alias, wh string) *kargoapi.Freight {
	return &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: project},
		Alias:      alias,
		Origin:     kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: wh},
	}
}

func TestStore_CreateGet(t *testing.T) {
	s := newTestStore(t)

	now := metav1.Now()
	f := &kargoapi.Freight{
		ObjectMeta:   metav1.ObjectMeta{Namespace: "p"},
		Origin:       kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: "w1"},
		DiscoveredAt: &now,
		Commits: []kargoapi.GitCommit{{
			RepoURL: "https://example.com/repo.git",
			ID:      "abc123",
		}},
		Images: []kargoapi.Image{{
			RepoURL: "example.com/image",
			Tag:     "v1.0.0",
			Digest:  "sha256:abc",
		}},
		Charts: []kargoapi.Chart{{
			RepoURL: "https://example.com/charts",
			Name:    "mychart",
			Version: "1.2.3",
		}},
		Artifacts: []kargoapi.ArtifactReference{{
			ArtifactType:     "some-type",
			SubscriptionName: "sub",
			Version:          "1",
		}},
	}
	require.NoError(t, s.Create(t.Context(), f))
	require.NotEmpty(t, f.Name)
	require.NotEmpty(t, f.Alias)
	require.Equal(t, f.Alias, f.Labels[kargoapi.LabelKeyAlias])

	got, err := s.Get(t.Context(), "p", f.Name)
	require.NoError(t, err)
	require.Equal(t, f.Name, got.Name)
	require.Equal(t, f.Alias, got.Alias)
	require.Equal(t, f.Labels, got.Labels)
	require.Equal(t, f.Origin, got.Origin)
	require.Equal(t, f.Commits, got.Commits)
	require.Equal(t, f.Images, got.Images)
	require.Equal(t, f.Charts, got.Charts)
	require.Equal(t, f.Artifacts, got.Artifacts)
	require.NotNil(t, got.DiscoveredAt)
	require.WithinDuration(t, f.DiscoveredAt.Time, got.DiscoveredAt.Time, time.Second)
	require.Equal(t, "Freight", got.Kind)
	require.Equal(t, "1", got.ResourceVersion)
}

func TestStore_Create_Conflicts(t *testing.T) {
	s := newTestStore(t)

	t.Run("duplicate name is rejected", func(t *testing.T) {
		f1 := newFreight("p", "", "w1")
		require.NoError(t, s.Create(t.Context(), f1))

		// Same project + origin (and no other distinguishing content) yields
		// the same generated name.
		f2 := newFreight("p", "", "w1")
		err := s.Create(t.Context(), f2)
		require.ErrorIs(t, err, freight.ErrAlreadyExists)
	})

	t.Run("caller-supplied alias conflict is rejected", func(t *testing.T) {
		f1 := newFreight("p", "taken-alias", "w2")
		require.NoError(t, s.Create(t.Context(), f1))

		f2 := newFreight("p", "taken-alias", "w3")
		err := s.Create(t.Context(), f2)
		require.ErrorIs(t, err, freight.ErrAliasConflict)
	})

	t.Run("generated alias conflict is retried", func(t *testing.T) {
		f1 := newFreight("p", "x", "w4")
		require.NoError(t, s.Create(t.Context(), f1))

		st, ok := s.(*store)
		require.True(t, ok)
		var calls int
		st.newAlias = func() string {
			calls++
			if calls == 1 {
				return "x"
			}
			return "y"
		}

		f2 := newFreight("p", "", "w5")
		require.NoError(t, s.Create(t.Context(), f2))
		require.Equal(t, "y", f2.Alias)
	})
}

func TestStore_GetByAlias(t *testing.T) {
	s := newTestStore(t)

	f := newFreight("p", "shiny-otter", "w1")
	require.NoError(t, s.Create(t.Context(), f))

	got, err := s.GetByAlias(t.Context(), "p", "shiny-otter")
	require.NoError(t, err)
	require.Equal(t, f.Name, got.Name)

	_, err = s.GetByAlias(t.Context(), "p", "nope")
	require.ErrorIs(t, err, freight.ErrNotFound)
}

func TestStore_List(t *testing.T) {
	s := newTestStore(t)

	// mk gives each piece of Freight a commit whose ID is its label, so that
	// freight.GenerateID (which hashes origin + commits/images/charts/
	// artifacts, not project or name) assigns each a distinct name -- Create
	// applies freight.Prepare, which always overwrites whatever Name is set
	// here.
	mk := func(label, wh string) *kargoapi.Freight {
		return &kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{Namespace: "list-project"},
			Origin:     kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: wh},
			Commits:    []kargoapi.GitCommit{{RepoURL: "https://example.com/repo.git", ID: label}},
		}
	}
	a := mk("a", "w1")
	b := mk("b", "w1")
	c := mk("c", "w2")

	nameToLabel := map[string]string{}
	for label, f := range map[string]*kargoapi.Freight{"a": a, "b": b, "c": c} {
		require.NoError(t, s.Create(t.Context(), f))
		nameToLabel[f.Name] = label
	}

	// Persist status separately -- Create resets it.
	_, err := s.UpdateStatus(t.Context(), "list-project", a.Name, func(st *kargoapi.FreightStatus) error {
		st.AddVerifiedStage("s1", time.Now())
		return nil
	})
	require.NoError(t, err)
	_, err = s.UpdateStatus(t.Context(), "list-project", b.Name, func(st *kargoapi.FreightStatus) error {
		st.AddVerifiedStage("s1", time.Now())
		st.AddVerifiedStage("s2", time.Now())
		return nil
	})
	require.NoError(t, err)
	_, err = s.UpdateStatus(t.Context(), "list-project", c.Name, func(st *kargoapi.FreightStatus) error {
		st.CurrentlyIn = map[string]kargoapi.CurrentStage{"s3": {}}
		st.ApprovedFor = map[string]kargoapi.ApprovedStage{"s4": {}}
		return nil
	})
	require.NoError(t, err)

	testCases := []struct {
		name string
		opts freight.ListOptions
		want []string
	}{
		{name: "all", want: []string{"a", "b", "c"}},
		{name: "by warehouse", opts: freight.ListOptions{Warehouse: "w1"}, want: []string{"a", "b"}},
		{name: "by warehouse no match", opts: freight.ListOptions{Warehouse: "nope"}, want: nil},
		{name: "origins", opts: freight.ListOptions{Origins: []string{"w2"}}, want: []string{"c"}},
		{name: "origins multi", opts: freight.ListOptions{Origins: []string{"w1", "w2"}}, want: []string{"a", "b", "c"}},
		{name: "verified in one", opts: freight.ListOptions{VerifiedIn: []string{"s1"}}, want: []string{"a", "b"}},
		{
			name: "verified in all requires both",
			opts: freight.ListOptions{VerifiedIn: []string{"s1", "s2"}},
			want: []string{"b"},
		},
		{name: "currently in", opts: freight.ListOptions{CurrentlyIn: "s3"}, want: []string{"c"}},
		{name: "approved for", opts: freight.ListOptions{ApprovedFor: "s4"}, want: []string{"c"}},
		{name: "combined no match", opts: freight.ListOptions{Warehouse: "w1", CurrentlyIn: "s3"}, want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.List(t.Context(), "list-project", tc.opts)
			require.NoError(t, err)
			labels := make([]string, len(got))
			for i, f := range got {
				labels[i] = nameToLabel[f.Name]
			}
			if tc.want == nil {
				require.Empty(t, labels)
			} else {
				// Real Freight names are content hashes, not the "a"/"b"/"c"
				// labels, so DB ordering by name does not correspond to label
				// order -- only set membership is asserted here.
				require.ElementsMatch(t, tc.want, labels)
			}
		})
	}

	t.Run("scoped to project", func(t *testing.T) {
		got, err := s.List(t.Context(), "other-project", freight.ListOptions{})
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestStore_UpdateStatus_ConcurrentConflicts(t *testing.T) {
	s := newTestStore(t)

	f := newFreight("concurrent-project", "", "w1")
	require.NoError(t, s.Create(t.Context(), f))

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := stageKey(i)
			_, err := s.UpdateStatus(
				t.Context(),
				"concurrent-project",
				f.Name,
				func(st *kargoapi.FreightStatus) error {
					if st.ApprovedFor == nil {
						st.ApprovedFor = make(map[string]kargoapi.ApprovedStage, 1)
					}
					st.ApprovedFor[key] = kargoapi.ApprovedStage{}
					return nil
				},
			)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	got, err := s.Get(t.Context(), "concurrent-project", f.Name)
	require.NoError(t, err)
	require.Len(t, got.Status.ApprovedFor, n)
	require.Equal(t, "21", got.ResourceVersion)
}

// stageKey returns a distinct, deterministic key for goroutine i to add to
// status.approvedFor.
func stageKey(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "stage-" + string(letters[i%len(letters)]) + string(rune('0'+i/len(letters)))
}

func TestStore_UpdateAlias(t *testing.T) {
	s := newTestStore(t)

	f1 := newFreight("alias-project", "taken", "w1")
	require.NoError(t, s.Create(t.Context(), f1))
	// A distinct warehouse origin ensures f2 gets a different generated name
	// than f1 (freight.GenerateID hashes origin + commits/images/etc., not
	// project or the caller-supplied name).
	f2 := newFreight("alias-project", "free", "w2")
	require.NoError(t, s.Create(t.Context(), f2))

	got, err := s.UpdateAlias(t.Context(), "alias-project", f2.Name, "renamed")
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Alias)
	require.Equal(t, "renamed", got.Labels[kargoapi.LabelKeyAlias])

	_, err = s.UpdateAlias(t.Context(), "alias-project", f2.Name, "taken")
	require.ErrorIs(t, err, freight.ErrAliasConflict)

	_, err = s.UpdateAlias(t.Context(), "alias-project", "nope", "whatever")
	require.ErrorIs(t, err, freight.ErrNotFound)
}

func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)

	f := newFreight("delete-project", "", "w1")
	require.NoError(t, s.Create(t.Context(), f))

	require.NoError(t, s.Delete(t.Context(), "delete-project", f.Name))
	_, err := s.Get(t.Context(), "delete-project", f.Name)
	require.ErrorIs(t, err, freight.ErrNotFound)

	err = s.Delete(t.Context(), "delete-project", f.Name)
	require.ErrorIs(t, err, freight.ErrNotFound)
}

func TestStore_DeleteProject(t *testing.T) {
	s := newTestStore(t)

	for _, wh := range []string{"w1", "w2"} {
		require.NoError(t, s.Create(t.Context(), newFreight("doomed-project", "", wh)))
	}
	survivor := newFreight("other-project", "", "w1")
	require.NoError(t, s.Create(t.Context(), survivor))

	require.NoError(t, s.DeleteProject(t.Context(), "doomed-project"))

	list, err := s.List(t.Context(), "doomed-project", freight.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = s.Get(t.Context(), "other-project", survivor.Name)
	require.NoError(t, err)

	// Deleting a project with no Freight is not an error.
	require.NoError(t, s.DeleteProject(t.Context(), "doomed-project"))
}

func TestStore_Watch(t *testing.T) {
	s := newTestStore(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := s.Watch(ctx, "watch-project")
	require.NoError(t, err)

	otherCh, err := s.Watch(ctx, "other")
	require.NoError(t, err)

	f := newFreight("watch-project", "", "w1")
	require.NoError(t, s.Create(t.Context(), f))

	select {
	case ev := <-ch:
		require.Equal(t, watch.Added, ev.Type)
		require.Equal(t, f.Name, ev.Object.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Added event")
	}

	_, err = s.UpdateAlias(t.Context(), "watch-project", f.Name, "renamed")
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, watch.Modified, ev.Type)
		require.Equal(t, f.Name, ev.Object.Name)
		require.Equal(t, "renamed", ev.Object.Alias)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Modified event")
	}

	require.NoError(t, s.Delete(t.Context(), "watch-project", f.Name))

	select {
	case ev := <-ch:
		require.Equal(t, watch.Deleted, ev.Type)
		require.Equal(t, f.Name, ev.Object.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Deleted event")
	}

	select {
	case ev := <-otherCh:
		t.Fatalf("unexpected event on unrelated project watch: %+v", ev)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestStore_Watch_DoesNotLeakListeningConnection verifies that a watch's
// LISTENing connection never comes from, or ends up in, the shared pool. A
// watch holding a pooled connection for its lifetime would starve every
// other caller once enough watches were open, and a LISTENing connection
// returned to the pool would hand a still-subscribed connection to the next
// unrelated caller.
func TestStore_Watch_DoesNotLeakListeningConnection(t *testing.T) {
	s := newTestStore(t)
	st, ok := s.(*store)
	require.True(t, ok)

	totalBefore := st.pool.Stat().TotalConns()

	ctx, cancel := context.WithCancel(t.Context())
	ch, err := s.Watch(ctx, "watch-release-project")
	require.NoError(t, err)

	// Give WaitForNotification a moment to actually start blocking before
	// cancellation, so this exercises the same code path a real shutdown
	// would.
	time.Sleep(100 * time.Millisecond)
	require.Zero(
		t,
		st.pool.Stat().AcquiredConns(),
		"an open watch must not hold a pooled connection",
	)
	cancel()

	select {
	case _, open := <-ch:
		require.False(t, open, "expected the watch channel to be closed")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch channel to close")
	}

	require.LessOrEqual(
		t,
		st.pool.Stat().TotalConns(),
		totalBefore,
		"the watch must not have grown the pool",
	)

	// Acquire every connection the pool can hand out at once (rather than
	// acquiring and releasing one at a time, which could repeatedly hand back
	// the same idle connection) so that every connection the pool currently
	// holds gets inspected.
	maxConns := int(st.pool.Stat().MaxConns())
	conns := make([]*pgxpool.Conn, 0, maxConns)
	defer func() {
		for _, c := range conns {
			c.Release()
		}
	}()
	for i := 0; i < maxConns; i++ {
		c, err := st.pool.Acquire(t.Context())
		require.NoError(t, err)
		conns = append(conns, c)
	}

	for _, c := range conns {
		rows, err := c.Query(t.Context(), "SELECT channel FROM pg_listening_channels() AS channel")
		require.NoError(t, err)
		var channels []string
		for rows.Next() {
			var name string
			require.NoError(t, rows.Scan(&name))
			channels = append(channels, name)
		}
		rows.Close()
		require.NoError(t, rows.Err())
		require.Empty(t, channels, "pooled connection must not still be subscribed to a channel")
	}
}

// TestStore_Watch_URLWithPoolParams verifies that Watch can open its
// connection when the database URL carries pgxpool-only parameters, which a
// plain connection would reject.
func TestStore_Watch_URLWithPoolParams(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	u, err := neturl.Parse(url)
	require.NoError(t, err)
	q := u.Query()
	q.Set("pool_max_conns", "2")
	u.RawQuery = q.Encode()

	s, err := NewStore(t.Context(), Config{URL: u.String()})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = s.Watch(ctx, "pool-params-project")
	require.NoError(t, err)
}
