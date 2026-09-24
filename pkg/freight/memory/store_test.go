package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

func newFreight(project, alias string, wh string) *kargoapi.Freight {
	return &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: project},
		Alias:      alias,
		Origin:     kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: wh},
	}
}

func TestStore_Create(t *testing.T) {
	t.Parallel()

	t.Run("assigns name and alias", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		f := newFreight("p", "", "w1")
		require.NoError(t, s.Create(t.Context(), f))
		require.NotEmpty(t, f.Name)
		require.NotEmpty(t, f.Alias)
		require.Equal(t, f.Alias, f.Labels[kargoapi.LabelKeyAlias])

		got, err := s.Get(t.Context(), "p", f.Name)
		require.NoError(t, err)
		require.Equal(t, f.Name, got.Name)
	})

	t.Run("rejects duplicate", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		f1 := newFreight("p", "", "w1")
		require.NoError(t, s.Create(t.Context(), f1))

		// Same project + origin (and no other distinguishing content) yields
		// the same generated name.
		f2 := newFreight("p", "", "w1")
		err := s.Create(t.Context(), f2)
		require.ErrorIs(t, err, freight.ErrAlreadyExists)
	})

	t.Run("rejects caller-supplied alias conflict", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		f1 := newFreight("p", "taken", "w1")
		require.NoError(t, s.Create(t.Context(), f1))

		f2 := newFreight("p", "taken", "w2")
		err := s.Create(t.Context(), f2)
		require.ErrorIs(t, err, freight.ErrAliasConflict)
	})

	t.Run("regenerates alias on collision", func(t *testing.T) {
		t.Parallel()
		s := NewStore(&kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "seed"},
			Alias:      "taken",
		})
		var calls int
		s.newAlias = func() string {
			calls++
			if calls == 1 {
				return "taken"
			}
			return "fresh"
		}

		f := newFreight("p", "", "w1")
		require.NoError(t, s.Create(t.Context(), f))
		require.Equal(t, "fresh", f.Alias)
		require.Equal(t, 2, calls)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.Create = func(context.Context, *kargoapi.Freight) error { return sentinel }
		err := s.Create(t.Context(), newFreight("p", "", "w1"))
		require.Same(t, sentinel, err)
	})
}

func TestStore_Get(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		_, err := s.Get(t.Context(), "p", "nope")
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}}
		s := NewStore(seed)
		got, err := s.Get(t.Context(), "p", "a")
		require.NoError(t, err)
		require.Equal(t, "a", got.Name)
		// Returned object must be a copy, not the stored one.
		got.Name = "mutated"
		got2, err := s.Get(t.Context(), "p", "a")
		require.NoError(t, err)
		require.Equal(t, "a", got2.Name)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.Get = func(context.Context, string, string) error { return sentinel }
		_, err := s.Get(t.Context(), "p", "a")
		require.Same(t, sentinel, err)
	})
}

func TestStore_GetByAlias(t *testing.T) {
	t.Parallel()

	seed := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"},
		Alias:      "shiny-otter",
	}
	s := NewStore(seed)

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		got, err := s.GetByAlias(t.Context(), "p", "shiny-otter")
		require.NoError(t, err)
		require.Equal(t, "a", got.Name)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, err := s.GetByAlias(t.Context(), "p", "nope")
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.GetByAlias = func(context.Context, string, string) error { return sentinel }
		_, err := s.GetByAlias(t.Context(), "p", "a")
		require.Same(t, sentinel, err)
	})
}

func TestStore_List(t *testing.T) {
	t.Parallel()
	mk := func(name, wh string, verified ...string) *kargoapi.Freight {
		f := &kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: name},
			Origin:     kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: wh},
		}
		for _, st := range verified {
			f.Status.AddVerifiedStage(st, time.Now())
		}
		return f
	}
	a := mk("a", "w1", "s1")
	b := mk("b", "w1", "s1", "s2")
	c := mk("c", "w2")
	c.Status.CurrentlyIn = map[string]kargoapi.CurrentStage{"s3": {}}
	c.Status.ApprovedFor = map[string]kargoapi.ApprovedStage{"s4": {}}

	s := NewStore(a, b, c)

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
			t.Parallel()
			got, err := s.List(t.Context(), "p", tc.opts)
			require.NoError(t, err)
			names := make([]string, len(got))
			for i, f := range got {
				names[i] = f.Name
			}
			if tc.want == nil {
				require.Empty(t, names)
			} else {
				require.Equal(t, tc.want, names)
			}
		})
	}

	t.Run("scoped to project", func(t *testing.T) {
		t.Parallel()
		got, err := s.List(t.Context(), "other-project", freight.ListOptions{})
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		errStore := NewStore()
		sentinel := errors.New("boom")
		errStore.Hooks.List = func(context.Context, string, freight.ListOptions) error { return sentinel }
		_, err := errStore.List(t.Context(), "p", freight.ListOptions{})
		require.Same(t, sentinel, err)
	})
}

func TestStore_UpdateStatus(t *testing.T) {
	t.Parallel()

	t.Run("increments resourceVersion and applies mutation", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a", ResourceVersion: "5"},
			Status:     kargoapi.FreightStatus{},
		}
		s := NewStore(seed)
		got, err := s.UpdateStatus(t.Context(), "p", "a", func(st *kargoapi.FreightStatus) error {
			st.AddVerifiedStage("s1", time.Now())
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, "6", got.ResourceVersion)
		require.Contains(t, got.Status.VerifiedIn, "s1")

		// Persisted.
		persisted, err := s.Get(t.Context(), "p", "a")
		require.NoError(t, err)
		require.Equal(t, "6", persisted.ResourceVersion)
		require.Contains(t, persisted.Status.VerifiedIn, "s1")
	})

	t.Run("defaults unparsable resourceVersion to 0", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{
			ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a", ResourceVersion: "not-a-number"},
		}
		s := NewStore(seed)
		got, err := s.UpdateStatus(t.Context(), "p", "a", func(*kargoapi.FreightStatus) error { return nil })
		require.NoError(t, err)
		require.Equal(t, "1", got.ResourceVersion)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		_, err := s.UpdateStatus(t.Context(), "p", "nope", func(*kargoapi.FreightStatus) error { return nil })
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("mutate error propagates", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}}
		s := NewStore(seed)
		sentinel := errors.New("bad mutation")
		_, err := s.UpdateStatus(t.Context(), "p", "a", func(*kargoapi.FreightStatus) error { return sentinel })
		require.Same(t, sentinel, err)

		// Not persisted.
		persisted, err := s.Get(t.Context(), "p", "a")
		require.NoError(t, err)
		require.Equal(t, "", persisted.ResourceVersion)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.UpdateStatus = func(context.Context, string, string) error { return sentinel }
		_, err := s.UpdateStatus(t.Context(), "p", "a", func(*kargoapi.FreightStatus) error { return nil })
		require.Same(t, sentinel, err)
	})
}

func TestStore_UpdateAlias(t *testing.T) {
	t.Parallel()

	t.Run("changes alias and label, increments version", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a", ResourceVersion: "1"}}
		s := NewStore(seed)
		got, err := s.UpdateAlias(t.Context(), "p", "a", "new-alias")
		require.NoError(t, err)
		require.Equal(t, "new-alias", got.Alias)
		require.Equal(t, "new-alias", got.Labels[kargoapi.LabelKeyAlias])
		require.Equal(t, "2", got.ResourceVersion)
	})

	t.Run("conflict", func(t *testing.T) {
		t.Parallel()
		s := NewStore(
			&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}, Alias: "taken"},
			&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "b"}, Alias: "free"},
		)
		_, err := s.UpdateAlias(t.Context(), "p", "b", "taken")
		require.ErrorIs(t, err, freight.ErrAliasConflict)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		_, err := s.UpdateAlias(t.Context(), "p", "nope", "alias")
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.UpdateAlias = func(context.Context, string, string, string) error { return sentinel }
		_, err := s.UpdateAlias(t.Context(), "p", "a", "alias")
		require.Same(t, sentinel, err)
	})
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()

	t.Run("deletes then get returns not found", func(t *testing.T) {
		t.Parallel()
		seed := &kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}}
		s := NewStore(seed)
		require.NoError(t, s.Delete(t.Context(), "p", "a"))
		_, err := s.Get(t.Context(), "p", "a")
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		err := s.Delete(t.Context(), "p", "nope")
		require.ErrorIs(t, err, freight.ErrNotFound)
	})

	t.Run("hook error returned verbatim", func(t *testing.T) {
		t.Parallel()
		s := NewStore()
		sentinel := errors.New("boom")
		s.Hooks.Delete = func(context.Context, string, string) error { return sentinel }
		err := s.Delete(t.Context(), "p", "a")
		require.Same(t, sentinel, err)
	})
}

func TestStore_DeleteProject(t *testing.T) {
	t.Parallel()

	t.Run("deletes only the project's Freight and emits events", func(t *testing.T) {
		t.Parallel()
		s := NewStore(
			&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}},
			&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "b"}},
			&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "other", Name: "a"}},
		)
		ch, err := s.Watch(t.Context(), "p")
		require.NoError(t, err)

		require.NoError(t, s.DeleteProject(t.Context(), "p"))

		deleted := map[string]struct{}{}
		for range 2 {
			select {
			case ev := <-ch:
				require.Equal(t, watch.Deleted, ev.Type)
				deleted[ev.Object.Name] = struct{}{}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for Deleted event")
			}
		}
		require.Equal(t, map[string]struct{}{"a": {}, "b": {}}, deleted)

		list, err := s.List(t.Context(), "p", freight.ListOptions{})
		require.NoError(t, err)
		require.Empty(t, list)
		_, err = s.Get(t.Context(), "other", "a")
		require.NoError(t, err)
	})

	t.Run("no Freight in project", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, NewStore().DeleteProject(t.Context(), "p"))
	})

	t.Run("hook error short-circuits", func(t *testing.T) {
		t.Parallel()
		s := NewStore(&kargoapi.Freight{ObjectMeta: metav1.ObjectMeta{Namespace: "p", Name: "a"}})
		sentinel := errors.New("boom")
		s.Hooks.DeleteProject = func(context.Context, string) error { return sentinel }
		require.Same(t, sentinel, s.DeleteProject(t.Context(), "p"))
		_, err := s.Get(t.Context(), "p", "a")
		require.NoError(t, err)
	})
}

func TestStore_Watch(t *testing.T) {
	t.Parallel()

	s := NewStore()
	ctx, cancel := context.WithCancel(t.Context())
	ch, err := s.Watch(ctx, "p")
	require.NoError(t, err)

	f := newFreight("p", "", "w1")
	require.NoError(t, s.Create(t.Context(), f))

	select {
	case ev := <-ch:
		require.Equal(t, watch.Added, ev.Type)
		require.Equal(t, f.Name, ev.Object.Name)
		require.True(t, ev.Changes.IsEmpty())
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Added event")
	}

	_, err = s.UpdateStatus(t.Context(), "p", f.Name, func(st *kargoapi.FreightStatus) error {
		st.AddVerifiedStage("s1", time.Now())
		return nil
	})
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, watch.Modified, ev.Type)
		require.Equal(t, []string{"s1"}, ev.Changes.NewlyVerifiedIn)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Modified event")
	}

	_, err = s.UpdateAlias(t.Context(), "p", f.Name, "renamed")
	require.NoError(t, err)

	select {
	case ev := <-ch:
		require.Equal(t, watch.Modified, ev.Type)
		require.Equal(t, "renamed", ev.Object.Alias)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Modified event")
	}

	require.NoError(t, s.Delete(t.Context(), "p", f.Name))

	select {
	case ev := <-ch:
		require.Equal(t, watch.Deleted, ev.Type)
		require.Equal(t, f.Name, ev.Object.Name)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Deleted event")
	}

	cancel()
	require.Eventually(t, func() bool {
		select {
		case _, ok := <-ch:
			return !ok
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestStore_Watch_scopedToProject(t *testing.T) {
	t.Parallel()

	s := NewStore()
	ch, err := s.Watch(t.Context(), "p")
	require.NoError(t, err)

	require.NoError(t, s.Create(t.Context(), newFreight("other", "", "w1")))

	select {
	case ev := <-ch:
		t.Fatalf("unexpected event for unrelated project: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestStore_Watch_AllProjects(t *testing.T) {
	t.Parallel()

	s := NewStore()
	ch, err := s.Watch(t.Context(), "")
	require.NoError(t, err)

	f1 := newFreight("p1", "", "w1")
	require.NoError(t, s.Create(t.Context(), f1))
	f2 := newFreight("p2", "", "w1")
	require.NoError(t, s.Create(t.Context(), f2))

	seen := map[string]struct{}{}
	for range 2 {
		select {
		case ev := <-ch:
			require.Equal(t, watch.Added, ev.Type)
			seen[ev.Object.Namespace] = struct{}{}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for Added event")
		}
	}
	require.Equal(t, map[string]struct{}{"p1": {}, "p2": {}}, seen)
}
