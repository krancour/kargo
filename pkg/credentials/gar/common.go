package gar

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"time"
)

const (
	accessTokenUsername    = "oauth2accesstoken"
	tokenCacheExpiryMargin = 5 * time.Minute

	// tokenAcquisitionTimeout bounds a single token acquisition. Because an
	// acquisition executes under a context detached from any caller's, this is
	// the only thing bounding its duration. It is generous because it serves only
	// as a fail-safe: exceeding it fails every caller waiting on that
	// acquisition, and no fresh acquisition for the same key can begin until it
	// returns.
	tokenAcquisitionTimeout = 30 * time.Second
)

var (
	gcrURLRegex = regexp.MustCompile(`^(?:.+\.)?gcr\.io/`) // Legacy
	garURLRegex = regexp.MustCompile(`^.+-docker\.pkg\.dev/`)
)

func tokenCacheKey(key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}
