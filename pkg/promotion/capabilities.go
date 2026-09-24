package promotion

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/akuity/kargo/pkg/credentials"
	"github.com/akuity/kargo/pkg/freight"
)

// StepRunnerCapabilities is a bundle of any special dependencies that may be
// injected into StepRunner implementations to grant them specific capabilities
// they may otherwise lack.
type StepRunnerCapabilities struct {
	KargoClient client.Client
	// FreightStore is set together with KargoClient when a StepRunner requires
	// StepCapabilityAccessControlPlane.
	FreightStore    freight.Store
	ArgoCDClient    client.Client
	CredsDB         credentials.Database
	GitUserResolver GitUserResolver
}
