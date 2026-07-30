package ecr

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/patrickmn/go-cache"
	"golang.org/x/sync/singleflight"

	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
	"github.com/akuity/kargo/pkg/logging"
)

const (
	regionKey = "awsRegion"
	idKey     = "awsAccessKeyID"
	secretKey = "awsSecretAccessKey"
)

func init() {
	if provider := NewAccessKeyProvider(); provider != nil {
		credentials.DefaultProviderRegistry.MustRegister(
			credentials.ProviderRegistration{
				Predicate: provider.Supports,
				Value:     provider,
			},
		)
	}
}

type AccessKeyProvider struct {
	tokenCache expiring.Cache

	// tokenGroup coalesces concurrent acquisitions of the token for any given
	// cache key into a single acquisition whose result is shared by all callers.
	tokenGroup singleflight.Group

	getAuthTokenFn func(
		ctx context.Context,
		region string,
		accessKeyID string,
		secretAccessKey string,
	) (string, time.Time, error)
}

func NewAccessKeyProvider() credentials.Provider {
	p := &AccessKeyProvider{
		tokenCache: cache.New(
			// Tokens live for 12 hours. We'll hang on to them for 10 by default.
			// When the actual token expiry is available, it is used (minus a
			// safety margin) instead of this default.
			10*time.Hour, // Default ttl for each entry
			time.Hour,    // Cleanup interval
		),
	}
	p.getAuthTokenFn = p.getAuthToken
	return p
}

func (p *AccessKeyProvider) Supports(
	_ context.Context,
	req credentials.Request,
) (bool, error) {
	if (req.Type != credentials.TypeImage && req.Type != credentials.TypeHelm) ||
		len(req.Data) == 0 {
		return false, nil
	}
	if matches := ecrURLRegex.FindStringSubmatch(req.RepoURL); len(matches) != 2 {
		return false, nil
	}
	return req.Data[regionKey] != nil &&
		req.Data[idKey] != nil &&
		req.Data[secretKey] != nil, nil
}

func (p *AccessKeyProvider) GetCredentials(
	ctx context.Context,
	req credentials.Request,
) (*credentials.Credentials, error) {
	region := string(req.Data[regionKey])
	accessKeyID := string(req.Data[idKey])
	secretAccessKey := string(req.Data[secretKey])
	cacheKey := tokenCacheKey(region, accessKeyID, secretAccessKey)

	logger := logging.LoggerFromContext(ctx).WithValues(
		"provider", "ecrAccessKey",
		"repoURL", req.RepoURL,
	)
	ctx = logging.ContextWithLogger(ctx, logger)

	// Check the cache for the token
	if entry, exists := p.tokenCache.Get(cacheKey); exists {
		logger.Debug("auth token cache hit")
		return decodeAuthToken(entry.(string)) // nolint: forcetypeassert
	}
	logger.Debug("auth token cache miss")

	// Cache miss, get a new token. All consumers of any given cache entry share
	// that entry, so they miss the cache in unison when it expires and, without
	// coordination, would each acquire their own token. Coalescing concurrent
	// acquisitions for the same cache key avoids that thundering herd.
	var encodedToken string
	ch := p.tokenGroup.DoChan(cacheKey, func() (any, error) {
		return p.getAndCacheAuthToken(
			ctx,
			region,
			accessKeyID,
			secretAccessKey,
			cacheKey,
		)
	})
	select {
	case <-ctx.Done():
		// See the comment in getAndCacheAuthToken for why we don't cancel the
		// acquisition here.
		return nil, fmt.Errorf(
			"auth token acquisition interrupted, cache refresh will continue in the background: %w",
			ctx.Err(),
		)
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		encodedToken = res.Val.(string) // nolint: forcetypeassert
	}

	// If we didn't get a token, we'll treat this as no credentials found
	if encodedToken == "" {
		return nil, nil
	}

	return decodeAuthToken(encodedToken)
}

// getAndCacheAuthToken obtains a new ECR authorization token, caches it, and
// returns it. It is intended to be executed within the provider's singleflight
// group.
func (p *AccessKeyProvider) getAndCacheAuthToken(
	orphanedCtx context.Context,
	region string,
	accessKeyID string,
	secretAccessKey string,
	cacheKey string,
) (string, error) {
	logger := logging.LoggerFromContext(orphanedCtx)

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

	encodedToken, expiry, err := p.getAuthTokenFn(
		orphanedCtx,
		region,
		accessKeyID,
		secretAccessKey,
	)
	if err != nil {
		return "", fmt.Errorf("error getting ECR auth token: %w", err)
	}

	// If we didn't get a token, we'll treat this as no credentials found
	if encodedToken == "" {
		return "", nil
	}
	logger.Debug("obtained new auth token")

	ttl := credentials.CalculateCacheTTL(expiry, tokenCacheExpiryMargin)
	logger.Debug(
		"caching auth token",
		"expiry", expiry,
		"ttl", ttl,
	)
	p.tokenCache.Set(cacheKey, encodedToken, ttl)

	return encodedToken, nil
}

// getAuthToken gets an ECR authorization token using the provided access key ID
// and secret access key. It returns the encoded token, which is a base64 string
// containing a username and password separated by a colon.
func (p *AccessKeyProvider) getAuthToken(
	ctx context.Context, region, accessKeyID, secretAccessKey string,
) (string, time.Time, error) {
	svc := ecr.NewFromConfig(aws.Config{
		Region:      region,
		Credentials: awscreds.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
	})

	output, err := svc.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("error getting ECR authorization token: %w", err)
	}

	if output == nil || len(output.AuthorizationData) == 0 {
		return "", time.Time{}, fmt.Errorf("no authorization data returned")
	}

	var expiry time.Time
	if output.AuthorizationData[0].ExpiresAt != nil {
		expiry = *output.AuthorizationData[0].ExpiresAt
	}

	if token := output.AuthorizationData[0].AuthorizationToken; token != nil {
		return *token, expiry, nil
	}

	return "", time.Time{}, fmt.Errorf("no authorization token returned")
}

// decodeAuthToken decodes an ECR authorization token by base64 decoding it and
// splitting it into a username and password.
func decodeAuthToken(token string) (*credentials.Credentials, error) {
	decodedToken, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("error decoding token: %w", err)
	}
	tokenParts := strings.SplitN(string(decodedToken), ":", 2)
	if len(tokenParts) != 2 {
		// This shouldn't ever happen
		return nil, fmt.Errorf("invalid token format")
	}
	return &credentials.Credentials{
		Username: tokenParts[0],
		Password: tokenParts[1],
	}, nil
}
