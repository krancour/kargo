package expiring

import "time"

// NewAlwaysMissing returns a Cache that retains nothing: every Set is a no-op
// and every Get is a miss.
func NewAlwaysMissing() Cache {
	return &alwaysMissing{}
}

type alwaysMissing struct{}

func (a *alwaysMissing) Get(string) (any, bool) { return nil, false }

func (a *alwaysMissing) Set(string, any, time.Duration) {}
