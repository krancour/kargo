// Package expiring abstracts the in-memory caching behavior that go-cache
// provides, so that a component depending on it can be given a different
// implementation. Unlike the sibling cache package, entries here carry a
// per-entry TTL and expire on their own.
package expiring

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// Cache is the subset of go-cache's behavior that callers depend on.
type Cache interface {
	// Get returns the value cached under the given key, and whether such a
	// value was found.
	Get(key string) (any, bool)
	// Set caches a value under the given key for the given TTL.
	Set(key string, value any, ttl time.Duration)
}

// go-cache is the production implementation.
var _ Cache = (*gocache.Cache)(nil)
