package api

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

// GenerateFreightID deterministically calculates a piece of Freight's ID based
// on its contents and returns it.
func GenerateFreightID(f *kargoapi.Freight) string {
	return freight.GenerateID(f)
}

// GetFreightByNameOrAlias returns a pointer to the Freight resource specified
// by the project, and name OR alias arguments. If no such resource is found,
// nil is returned instead.
func GetFreightByNameOrAlias(
	ctx context.Context,
	store freight.Store,
	project string,
	name string,
	alias string,
) (*kargoapi.Freight, error) {
	if name != "" {
		return GetFreight(
			ctx,
			store,
			types.NamespacedName{
				Namespace: project,
				Name:      name,
			},
		)
	}
	return GetFreightByAlias(ctx, store, project, alias)
}

// GetFreight returns a pointer to the Freight resource specified by the
// namespacedName argument. If no such resource is found, nil is returned
// instead.
func GetFreight(
	ctx context.Context,
	store freight.Store,
	namespacedName types.NamespacedName,
) (*kargoapi.Freight, error) {
	f, err := store.Get(ctx, namespacedName.Namespace, namespacedName.Name)
	if err != nil {
		if errors.Is(err, freight.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"error getting Freight %q in namespace %q: %w",
			namespacedName.Name,
			namespacedName.Namespace,
			err,
		)
	}
	return f, nil
}

// GetFreightByAlias returns a pointer to the Freight resource specified by the
// project and alias arguments. If no such resource is found, nil is returned
// instead.
func GetFreightByAlias(
	ctx context.Context,
	store freight.Store,
	project string,
	alias string,
) (*kargoapi.Freight, error) {
	f, err := store.GetByAlias(ctx, project, alias)
	if err != nil {
		if errors.Is(err, freight.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"error getting Freight with alias %q in namespace %q: %w",
			alias,
			project,
			err,
		)
	}
	return f, nil
}

// ListFreightByCurrentStage returns a list of Freight resources that think
// they're currently in use by the Stage specified.
func ListFreightByCurrentStage(
	ctx context.Context,
	store freight.Store,
	stage *kargoapi.Stage,
) ([]kargoapi.Freight, error) {
	items, err := store.List(
		ctx,
		stage.Namespace,
		freight.ListOptions{CurrentlyIn: stage.Name},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error listing Freight in namespace %q with current stage %q: %w",
			stage.Namespace,
			stage.Name,
			err,
		)
	}
	return items, nil
}
