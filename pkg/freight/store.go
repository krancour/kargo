// Package freight defines the storage interface for Freight, independent of
// the backend (Kubernetes or Postgres) that implements it.
package freight

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/watch"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound indicates that no Freight matching the given criteria could
	// be found.
	ErrNotFound = errors.New("freight not found")
	// ErrAlreadyExists indicates that a piece of Freight with the same
	// project and name already exists.
	ErrAlreadyExists = errors.New("freight already exists")
	// ErrAliasConflict indicates that the requested alias is already in use
	// by another piece of Freight in the same project.
	ErrAliasConflict = errors.New("freight alias already in use")
)

// ListOptions represents the criteria by which a list of Freight may be
// filtered.
type ListOptions struct {
	// Warehouse, when set, limits the list to Freight whose origin.name
	// matches this value.
	Warehouse string
	// Origins, when non-empty, limits the list to Freight whose origin.name
	// matches any of these values.
	Origins []string
	// CurrentlyIn, when set, limits the list to Freight for which this key
	// exists in status.currentlyIn.
	CurrentlyIn string
	// VerifiedIn, when non-empty, limits the list to Freight for which every
	// one of these keys exists in status.verifiedIn.
	VerifiedIn []string
	// ApprovedFor, when set, limits the list to Freight for which this key
	// exists in status.approvedFor.
	ApprovedFor string
}

// Event represents a change to a piece of Freight observed by Watch.
type Event struct {
	// Type indicates the kind of change that occurred.
	Type watch.EventType
	// Object is the piece of Freight the change pertains to.
	Object *kargoapi.Freight
}

// Store is an interface for the storage and retrieval of Freight.
type Store interface {
	// Create saves the given Freight. It returns ErrAlreadyExists if a piece
	// of Freight with the same project and name already exists, and
	// ErrAliasConflict if the caller supplied an alias that is already in use
	// by another piece of Freight in the same project. A generated alias that
	// collides is retried transparently and never surfaces as
	// ErrAliasConflict.
	Create(ctx context.Context, f *kargoapi.Freight) error
	// Get returns the Freight with the given project and name. It returns
	// ErrNotFound if no such Freight exists.
	Get(ctx context.Context, project, name string) (*kargoapi.Freight, error)
	// GetByAlias returns the Freight with the given project and alias. It
	// returns ErrNotFound if no such Freight exists.
	GetByAlias(ctx context.Context, project, alias string) (*kargoapi.Freight, error)
	// List returns the Freight in the given project that match opts.
	List(ctx context.Context, project string, opts ListOptions) ([]kargoapi.Freight, error)
	// UpdateStatus applies mutate to the status of the Freight with the given
	// project and name and persists the result. It returns ErrNotFound if no
	// such Freight exists.
	UpdateStatus(
		ctx context.Context,
		project, name string,
		mutate func(*kargoapi.FreightStatus) error,
	) (*kargoapi.Freight, error)
	// UpdateAlias changes the alias of the Freight with the given project and
	// name. It returns ErrNotFound if no such Freight exists, and
	// ErrAliasConflict if another Freight in the project already has that
	// alias.
	UpdateAlias(ctx context.Context, project, name, alias string) (*kargoapi.Freight, error)
	// Delete removes the Freight with the given project and name. It returns
	// ErrNotFound if no such Freight exists.
	Delete(ctx context.Context, project, name string) error
	// DeleteProject deletes every Freight in the project. It returns nil when
	// the project has no Freight.
	DeleteProject(ctx context.Context, project string) error
	// Watch returns a channel of Events reflecting changes to Freight in the
	// given project.
	Watch(ctx context.Context, project string) (<-chan Event, error)
}
