// Package memory provides an in-memory implementation of freight.Store,
// intended for use in tests in place of a fake Kubernetes client.
package memory

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"k8s.io/apimachinery/pkg/watch"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

// watchBufferSize is the size of the buffered channel returned by Watch. An
// event is dropped if the buffer is full when it is sent.
const watchBufferSize = 256

// Hooks lets tests inject errors into a Store's operations. A non-nil hook is
// invoked first; if it returns a non-nil error, that error is returned
// verbatim and the operation does not proceed.
type Hooks struct {
	Create        func(ctx context.Context, f *kargoapi.Freight) error
	Get           func(ctx context.Context, project, name string) error
	GetByAlias    func(ctx context.Context, project, alias string) error
	List          func(ctx context.Context, project string, opts freight.ListOptions) error
	UpdateStatus  func(ctx context.Context, project, name string) error
	UpdateAlias   func(ctx context.Context, project, name, alias string) error
	Delete        func(ctx context.Context, project, name string) error
	DeleteProject func(ctx context.Context, project string) error
}

// key uniquely identifies a piece of Freight by project (namespace) and name.
type key struct {
	project string
	name    string
}

// Store is an in-memory implementation of freight.Store.
type Store struct {
	// Hooks, when set, let tests inject errors into the Store's operations.
	Hooks Hooks

	mu       sync.RWMutex
	items    map[key]*kargoapi.Freight
	watchers map[string][]chan freight.Event
	newAlias func() string
}

// NewStore returns a new Store seeded with the given Freight. Seeds are
// deep-copied and stored exactly as given -- freight.Prepare is NOT applied
// to them, so tests may seed Freight with names, aliases, and resource
// versions of their own choosing.
func NewStore(seed ...*kargoapi.Freight) *Store {
	s := &Store{
		items:    make(map[key]*kargoapi.Freight, len(seed)),
		watchers: make(map[string][]chan freight.Event),
		newAlias: freight.NewAliasGenerator(),
	}
	for _, f := range seed {
		s.items[key{project: f.Namespace, name: f.Name}] = f.DeepCopy()
	}
	return s
}

var _ freight.Store = (*Store)(nil)

// Create implements freight.Store.
func (s *Store) Create(ctx context.Context, f *kargoapi.Freight) error {
	if s.Hooks.Create != nil {
		if err := s.Hooks.Create(ctx, f); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	callerAlias := f.Alias
	freight.Prepare(f, s.newAlias)

	k := key{project: f.Namespace, name: f.Name}
	if _, exists := s.items[k]; exists {
		return freight.ErrAlreadyExists
	}

	if callerAlias != "" {
		if s.aliasTakenLocked(f.Namespace, f.Alias, "") {
			return freight.ErrAliasConflict
		}
	} else {
		for s.aliasTakenLocked(f.Namespace, f.Alias, "") {
			f.Alias = s.newAlias()
			f.Labels[kargoapi.LabelKeyAlias] = f.Alias
		}
	}

	s.items[k] = f.DeepCopy()
	s.notifyLocked(f.Namespace, watch.Added, f)
	return nil
}

// Get implements freight.Store.
func (s *Store) Get(ctx context.Context, project, name string) (*kargoapi.Freight, error) {
	if s.Hooks.Get != nil {
		if err := s.Hooks.Get(ctx, project, name); err != nil {
			return nil, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	f, ok := s.items[key{project: project, name: name}]
	if !ok {
		return nil, freight.ErrNotFound
	}
	return f.DeepCopy(), nil
}

// GetByAlias implements freight.Store.
func (s *Store) GetByAlias(ctx context.Context, project, alias string) (*kargoapi.Freight, error) {
	if s.Hooks.GetByAlias != nil {
		if err := s.Hooks.GetByAlias(ctx, project, alias); err != nil {
			return nil, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for k, f := range s.items {
		if k.project == project && f.Alias == alias {
			return f.DeepCopy(), nil
		}
	}
	return nil, freight.ErrNotFound
}

// List implements freight.Store.
func (s *Store) List(
	ctx context.Context,
	project string,
	opts freight.ListOptions,
) ([]kargoapi.Freight, error) {
	if s.Hooks.List != nil {
		if err := s.Hooks.List(ctx, project, opts); err != nil {
			return nil, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var origins map[string]struct{}
	if len(opts.Origins) > 0 {
		origins = make(map[string]struct{}, len(opts.Origins))
		for _, o := range opts.Origins {
			origins[o] = struct{}{}
		}
	}

	var result []kargoapi.Freight
	for k, f := range s.items {
		if k.project != project {
			continue
		}
		if !matches(f, opts, origins) {
			continue
		}
		result = append(result, *f.DeepCopy())
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// matches reports whether f satisfies all criteria in opts. origins is a
// precomputed set derived from opts.Origins, or nil if opts.Origins is empty.
func matches(f *kargoapi.Freight, opts freight.ListOptions, origins map[string]struct{}) bool {
	if opts.Warehouse != "" &&
		(f.Origin.Kind != kargoapi.FreightOriginKindWarehouse || f.Origin.Name != opts.Warehouse) {
		return false
	}
	if origins != nil {
		if _, ok := origins[f.Origin.Name]; !ok {
			return false
		}
	}
	if opts.CurrentlyIn != "" {
		if _, ok := f.Status.CurrentlyIn[opts.CurrentlyIn]; !ok {
			return false
		}
	}
	for _, stage := range opts.VerifiedIn {
		if _, ok := f.Status.VerifiedIn[stage]; !ok {
			return false
		}
	}
	if opts.ApprovedFor != "" {
		if _, ok := f.Status.ApprovedFor[opts.ApprovedFor]; !ok {
			return false
		}
	}
	return true
}

// UpdateStatus implements freight.Store.
func (s *Store) UpdateStatus(
	ctx context.Context,
	project, name string,
	mutate func(*kargoapi.FreightStatus) error,
) (*kargoapi.Freight, error) {
	if s.Hooks.UpdateStatus != nil {
		if err := s.Hooks.UpdateStatus(ctx, project, name); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key{project: project, name: name}
	f, ok := s.items[k]
	if !ok {
		return nil, freight.ErrNotFound
	}

	updated := f.DeepCopy()
	if err := mutate(&updated.Status); err != nil {
		return nil, err
	}
	bumpVersion(updated)

	s.items[k] = updated
	s.notifyLocked(project, watch.Modified, updated)
	return updated.DeepCopy(), nil
}

// UpdateAlias implements freight.Store.
func (s *Store) UpdateAlias(ctx context.Context, project, name, alias string) (*kargoapi.Freight, error) {
	if s.Hooks.UpdateAlias != nil {
		if err := s.Hooks.UpdateAlias(ctx, project, name, alias); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key{project: project, name: name}
	f, ok := s.items[k]
	if !ok {
		return nil, freight.ErrNotFound
	}

	if s.aliasTakenLocked(project, alias, name) {
		return nil, freight.ErrAliasConflict
	}

	updated := f.DeepCopy()
	updated.Alias = alias
	if updated.Labels == nil {
		updated.Labels = make(map[string]string, 1)
	}
	updated.Labels[kargoapi.LabelKeyAlias] = alias
	bumpVersion(updated)

	s.items[k] = updated
	s.notifyLocked(project, watch.Modified, updated)
	return updated.DeepCopy(), nil
}

// Delete implements freight.Store.
func (s *Store) Delete(ctx context.Context, project, name string) error {
	if s.Hooks.Delete != nil {
		if err := s.Hooks.Delete(ctx, project, name); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key{project: project, name: name}
	f, ok := s.items[k]
	if !ok {
		return freight.ErrNotFound
	}

	delete(s.items, k)
	s.notifyLocked(project, watch.Deleted, f)
	return nil
}

// DeleteProject implements freight.Store.
func (s *Store) DeleteProject(ctx context.Context, project string) error {
	if s.Hooks.DeleteProject != nil {
		if err := s.Hooks.DeleteProject(ctx, project); err != nil {
			return err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for k, f := range s.items {
		if k.project != project {
			continue
		}
		delete(s.items, k)
		s.notifyLocked(project, watch.Deleted, f)
	}
	return nil
}

// Watch implements freight.Store.
func (s *Store) Watch(ctx context.Context, project string) (<-chan freight.Event, error) {
	ch := make(chan freight.Event, watchBufferSize)

	s.mu.Lock()
	s.watchers[project] = append(s.watchers[project], ch)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		watchers := s.watchers[project]
		for i, w := range watchers {
			if w == ch {
				s.watchers[project] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch, nil
}

// aliasTakenLocked reports whether alias is already in use by a piece of
// Freight in project other than the one named exceptName. The caller must
// hold s.mu (for reading or writing).
func (s *Store) aliasTakenLocked(project, alias, exceptName string) bool {
	for k, f := range s.items {
		if k.project == project && k.name != exceptName && f.Alias == alias {
			return true
		}
	}
	return false
}

// notifyLocked fans out an event to all watchers of project. The caller must
// hold s.mu for writing. A watcher whose buffer is full does not receive the
// event.
func (s *Store) notifyLocked(project string, t watch.EventType, f *kargoapi.Freight) {
	for _, ch := range s.watchers[project] {
		select {
		case ch <- freight.Event{Type: t, Object: f.DeepCopy()}:
		default:
		}
	}
}

// bumpVersion increments f's ResourceVersion. An empty or unparsable
// ResourceVersion is treated as 0.
func bumpVersion(f *kargoapi.Freight) {
	v, err := strconv.Atoi(f.ResourceVersion)
	if err != nil {
		v = 0
	}
	f.ResourceVersion = strconv.Itoa(v + 1)
}
