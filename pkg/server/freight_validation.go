package server

import (
	"fmt"
	"path"

	"k8s.io/apimachinery/pkg/util/validation/field"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/urls"
)

type artifactType string

func (a artifactType) FreightPath() string {
	switch a {
	case artifactTypeGit:
		return "commits"
	case artifactTypeImage:
		return "images"
	case artifactTypeChart:
		return "charts"
	default:
		return ""
	}
}

const (
	artifactTypeGit   artifactType = "git"
	artifactTypeImage artifactType = "image"
	artifactTypeChart artifactType = "chart"
)

type artifactSubscription struct {
	URL  string
	Type artifactType
}

// validateFreightArtifacts checks that the artifacts in the Freight are all
// subscribed to by the Warehouse. It returns an error if:
//
//   - An artifact in the Freight is not subscribed to by the Warehouse.
//   - An artifact for a subscription of the Warehouse is not found in the Freight.
//   - Multiple artifacts in the Freight correspond to the same subscription.
//
// TODO(krancour): A subscription is identified here by repository URL and type.
// Subscription name will need to be part of that key when Warehouses are
// permitted multiple subscriptions to the same repository.
//
// See https://github.com/akuity/kargo/issues/6724.
func validateFreightArtifacts(
	freight *kargoapi.Freight,
	warehouse *kargoapi.Warehouse,
) field.ErrorList {
	subscriptions := make(map[artifactSubscription]bool, len(warehouse.Spec.InternalSubscriptions))
	counts := make(map[artifactSubscription]int)

	// Collect all the subscriptions from the Warehouse.
	for _, repo := range warehouse.Spec.InternalSubscriptions {
		if repo.Git != nil {
			subscriptions[artifactSubscription{
				URL:  urls.NormalizeGit(repo.Git.RepoURL),
				Type: artifactTypeGit,
			}] = false
		}
		if repo.Image != nil {
			subscriptions[artifactSubscription{
				URL:  repo.Image.RepoURL,
				Type: artifactTypeImage,
			}] = false
		}
		if repo.Chart != nil {
			subscriptions[artifactSubscription{
				URL:  path.Join(urls.NormalizeChart(repo.Chart.RepoURL), repo.Chart.Name),
				Type: artifactTypeChart,
			}] = false
		}
	}

	var errs field.ErrorList

	// Mark the subscription as found for each artifact in the Freight, and count
	// the number of times each subscription is found.
	for _, commit := range freight.Commits {
		sub := artifactSubscription{
			URL:  urls.NormalizeGit(commit.RepoURL),
			Type: artifactTypeGit,
		}
		if _, ok := subscriptions[sub]; ok {
			subscriptions[sub] = true
			counts[sub]++
			continue
		}
		errs = append(
			errs,
			field.Invalid(
				field.NewPath("commits"),
				commit,
				fmt.Sprintf("no subscription found for Git repository in Warehouse %q", warehouse.Name),
			),
		)
	}

	for _, image := range freight.Images {
		sub := artifactSubscription{
			URL:  image.RepoURL,
			Type: artifactTypeImage,
		}
		if _, ok := subscriptions[sub]; ok {
			subscriptions[sub] = true
			counts[sub]++
			continue
		}
		errs = append(
			errs,
			field.Invalid(
				field.NewPath("images"),
				image,
				fmt.Sprintf("no subscription found for image repository in Warehouse %q", warehouse.Name),
			),
		)
	}

	for _, chart := range freight.Charts {
		sub := artifactSubscription{
			URL:  path.Join(urls.NormalizeChart(chart.RepoURL), chart.Name),
			Type: artifactTypeChart,
		}
		if _, ok := subscriptions[sub]; ok {
			subscriptions[sub] = true
			counts[sub]++
			continue
		}
		errs = append(
			errs,
			field.Invalid(
				field.NewPath("charts"),
				chart,
				fmt.Sprintf("no subscription found for Helm chart in Warehouse %q", warehouse.Name),
			),
		)
	}

	// Check that each subscription is found exactly once.
	for sub, found := range subscriptions {
		if !found {
			errs = append(
				errs,
				field.Invalid(
					field.NewPath(sub.Type.FreightPath()),
					nil,
					fmt.Sprintf(
						"no artifact found for subscription %q of Warehouse %q",
						sub.URL, warehouse.Name,
					),
				),
			)
			continue
		}
		if counts[sub] > 1 {
			errs = append(
				errs,
				field.Invalid(
					field.NewPath(sub.Type.FreightPath()),
					nil,
					fmt.Sprintf(
						"multiple artifacts found for subscription %q of Warehouse %q",
						sub.URL, warehouse.Name,
					),
				),
			)
		}
	}

	return errs
}
