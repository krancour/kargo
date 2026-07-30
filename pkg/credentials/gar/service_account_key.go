package gar

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/singleflight"

	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
	"github.com/akuity/kargo/pkg/logging"
)

const (
	serviceAccountKeyKey = "gcpServiceAccountKey"
	scopeStorageRead     = "https://www.googleapis.com/auth/devstorage.read_only"
)

func init() {
	if provider := NewServiceAccountKeyProvider(); provider != nil {
		credentials.DefaultProviderRegistry.MustRegister(
			credentials.ProviderRegistration{
				Predicate: provider.Supports,
				Value:     provider,
			},
		)
	}
}

type ServiceAccountKeyProvider struct {
	tokenCache expiring.Cache

	// tokenGroup coalesces concurrent acquisitions of the token for any given
	// cache key into a single acquisition whose result is shared by all callers.
	tokenGroup singleflight.Group

	getAccessTokenFn func(
		ctx context.Context,
		encodedServiceAccountKey string,
	) (*oauth2.Token, error)
}

func NewServiceAccountKeyProvider() credentials.Provider {
	p := &ServiceAccountKeyProvider{
		tokenCache: cache.New(
			// Access tokens live for one hour. We'll hang on to them for 40
			// minutes by default. When the actual token expiry is available, it
			// is used (minus a safety margin) instead of this default.
			40*time.Minute, // Default ttl for each entry
			time.Hour,      // Cleanup interval
		),
	}
	p.getAccessTokenFn = p.getAccessToken
	return p
}

func (p *ServiceAccountKeyProvider) Supports(
	_ context.Context,
	req credentials.Request,
) (bool, error) {
	if req.Type != credentials.TypeImage && req.Type != credentials.TypeHelm ||
		req.Data == nil ||
		req.Data[serviceAccountKeyKey] == nil {
		return false, nil
	}
	if !garURLRegex.MatchString(req.RepoURL) &&
		!gcrURLRegex.MatchString(req.RepoURL) {
		return false, nil
	}
	return true, nil
}

func (p *ServiceAccountKeyProvider) GetCredentials(
	ctx context.Context,
	req credentials.Request,
) (*credentials.Credentials, error) {
	encodedServiceAccountKey := string(req.Data[serviceAccountKeyKey])
	cacheKey := tokenCacheKey(encodedServiceAccountKey)

	logger := logging.LoggerFromContext(ctx).WithValues(
		"provider", "garServiceAccountKey",
		"repoURL", req.RepoURL,
	)
	ctx = logging.ContextWithLogger(ctx, logger)

	// Check the cache for the token
	if entry, exists := p.tokenCache.Get(cacheKey); exists {
		logger.Debug("access token cache hit")
		return &credentials.Credentials{
			Username: accessTokenUsername,
			Password: entry.(string), // nolint: forcetypeassert
		}, nil
	}
	logger.Debug("access token cache miss")

	// Cache miss, get a new token. All consumers of any given cache entry share
	// that entry, so they miss the cache in unison when it expires and, without
	// coordination, would each acquire their own token. Coalescing concurrent
	// acquisitions for the same cache key avoids that thundering herd.
	var accessToken string
	ch := p.tokenGroup.DoChan(cacheKey, func() (any, error) {
		return p.getAndCacheAccessToken(ctx, encodedServiceAccountKey, cacheKey)
	})
	select {
	case <-ctx.Done():
		// See the comment in getAndCacheAccessToken for why we don't cancel the
		// acquisition here.
		return nil, fmt.Errorf(
			"access token acquisition interrupted, cache refresh will continue in the background: %w",
			ctx.Err(),
		)
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		accessToken = res.Val.(string) // nolint: forcetypeassert
	}

	// If we didn't get a token, we'll treat this as no credentials found
	if accessToken == "" {
		return nil, nil
	}

	return &credentials.Credentials{
		Username: accessTokenUsername,
		Password: accessToken,
	}, nil
}

// getAndCacheAccessToken obtains a new GCP access token, caches it, and returns
// it. It is intended to be executed within the provider's singleflight group.
func (p *ServiceAccountKeyProvider) getAndCacheAccessToken(
	ctx context.Context,
	encodedServiceAccountKey string,
	cacheKey string,
) (string, error) {
	logger := logging.LoggerFromContext(ctx)

	// This runs under a context detached from any caller's. Its result is shared
	// by every caller waiting on it, so it must not be owned by whichever caller
	// happened to win the singleflight race -- that caller giving up would
	// otherwise fail all the others, whose own contexts are still live. Even if
	// every caller has abandoned it, finishing remains worthwhile, since any token
	// it obtains is cached for future callers. Since no caller's cancellation
	// bounds this work, it carries its own timeout. The logger belongs to the
	// winning caller and is carried over so that the calling context is not lost.
	orphanedCtx, cancel := context.WithTimeout(
		logging.ContextWithLogger(context.Background(), logger),
		tokenAcquisitionTimeout,
	)
	defer cancel()

	token, err := p.getAccessTokenFn(orphanedCtx, encodedServiceAccountKey)
	if err != nil {
		return "", fmt.Errorf("error getting GCP access token: %w", err)
	}

	// If we didn't get a token, we'll treat this as no credentials found
	if token == nil || token.AccessToken == "" {
		return "", nil
	}
	logger.Debug("obtained new access token")

	ttl := credentials.CalculateCacheTTL(token.Expiry, tokenCacheExpiryMargin)
	logger.Debug(
		"caching access token",
		"expiry", token.Expiry,
		"ttl", ttl,
	)
	p.tokenCache.Set(cacheKey, token.AccessToken, ttl)

	return token.AccessToken, nil
}

// getAccessToken returns a GCP access token retrieved using the provided base64
// encoded service account key. The access token is valid for one hour.
func (p *ServiceAccountKeyProvider) getAccessToken(
	ctx context.Context,
	encodedServiceAccountKey string,
) (*oauth2.Token, error) {
	decodedKey, err := base64.StdEncoding.DecodeString(encodedServiceAccountKey)
	if err != nil {
		return nil, fmt.Errorf("error decoding service account key: %w", err)
	}

	config, err := google.JWTConfigFromJSON(decodedKey, scopeStorageRead)
	if err != nil {
		return nil, fmt.Errorf("error parsing service account key: %w", err)
	}

	tokenSource := config.TokenSource(ctx)
	token, err := tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("error getting access token: %w", err)
	}
	return token, nil
}
