package credentials

import (
	"time"

	"github.com/akuity/kargo/pkg/cache/expiring"
)

// CalculateCacheTTL calculates the time-to-live for a cached credential based
// on the credential's expiry time and a safety margin. If the expiry time is
// zero (unknown) or the remaining time after subtracting the margin is not
// positive, it returns expiring.DefaultTTL, deferring to the cache's own
// default TTL.
func CalculateCacheTTL(expiry time.Time, margin time.Duration) time.Duration {
	ttl := expiring.DefaultTTL
	if !expiry.IsZero() {
		if remaining := time.Until(expiry) - margin; remaining > 0 {
			ttl = remaining
		}
	}
	return ttl
}
