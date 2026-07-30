package expiring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAlwaysMissing(t *testing.T) {
	c := NewAlwaysMissing()

	val, found := c.Get("key")
	require.False(t, found)
	require.Nil(t, val)

	// Setting a value changes nothing about what a subsequent Get returns.
	c.Set("key", "value", time.Hour)
	val, found = c.Get("key")
	require.False(t, found)
	require.Nil(t, val)
}
