package freight

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func TestPrepare(t *testing.T) {
	t.Parallel()
	f := &kargoapi.Freight{
		ObjectMeta: metav1.ObjectMeta{Namespace: "proj"},
		Origin:     kargoapi.FreightOrigin{Kind: kargoapi.FreightOriginKindWarehouse, Name: "wh"},
		Images:     []kargoapi.Image{{RepoURL: "example/img", Tag: "v1"}},
		Status:     kargoapi.FreightStatus{ApprovedFor: map[string]kargoapi.ApprovedStage{"x": {}}},
	}
	Prepare(f, func() string { return "brave-otter" })
	require.Equal(t, GenerateID(f), f.Name)
	require.Equal(t, "Freight", f.Kind)
	require.Equal(t, kargoapi.GroupVersion.String(), f.APIVersion)
	require.NotEmpty(t, f.UID)
	require.False(t, f.CreationTimestamp.IsZero())
	require.NotNil(t, f.DiscoveredAt)
	require.Equal(t, "brave-otter", f.Alias)
	require.Equal(t, "brave-otter", f.Labels[kargoapi.LabelKeyAlias])
	require.Equal(t, "1", f.ResourceVersion)
	require.Empty(t, f.Status.ApprovedFor)
}

func TestPrepare_keepsCallerAlias(t *testing.T) {
	t.Parallel()
	f := &kargoapi.Freight{Alias: "mine", Origin: kargoapi.FreightOrigin{Name: "wh"}}
	Prepare(f, func() string { t.Fatal("must not be called"); return "" })
	require.Equal(t, "mine", f.Alias)
	require.Equal(t, "mine", f.Labels[kargoapi.LabelKeyAlias])
}
