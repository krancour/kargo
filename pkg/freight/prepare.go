package freight

import (
	"github.com/google/uuid"
	"github.com/technosophos/moniker"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// NewAliasGenerator returns a function that generates a random,
// human-readable alias each time it is called.
func NewAliasGenerator() func() string {
	namer := moniker.New()
	return func() string { return namer.NameSep("-") }
}

// Prepare defaults the fields of f that must be set before it is persisted:
// it (re)calculates Name from f's contents, assigns a UID and creation
// timestamp if unset, backfills DiscoveredAt, assigns an alias (using
// newAlias if one was not already supplied by the caller), and resets
// ResourceVersion and Status to the values a newly created piece of Freight
// should have.
func Prepare(f *kargoapi.Freight, newAlias func() string) {
	f.TypeMeta = metav1.TypeMeta{
		APIVersion: kargoapi.GroupVersion.String(),
		Kind:       "Freight",
	}

	// Re-calculate ID in case it wasn't set correctly to begin with -- possible
	// when users create their own Freight.
	f.Name = GenerateID(f)

	if f.UID == "" {
		f.UID = types.UID(uuid.NewString())
	}

	if f.CreationTimestamp.IsZero() {
		f.CreationTimestamp = metav1.Now()
	}

	if f.DiscoveredAt == nil {
		f.DiscoveredAt = &f.CreationTimestamp
	}

	if f.Labels == nil {
		f.Labels = make(map[string]string, 1)
	}
	if f.Alias == "" {
		f.Alias = newAlias()
	}
	f.Labels[kargoapi.LabelKeyAlias] = f.Alias

	f.ResourceVersion = "1"
	f.Status = kargoapi.FreightStatus{}
}
