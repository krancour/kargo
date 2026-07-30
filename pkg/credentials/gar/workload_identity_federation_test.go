package gar

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/akuity/kargo/pkg/credentials"
)

func TestWorkloadIdentityFederationProvider_Supports(t *testing.T) {
	t.Parallel()

	const (
		fakeProjectID  = "test-project"
		fakeGCRRepoURL = "gcr.io/my-project/my-repo"
		fakeGARRepoURL = "us-central1-docker.pkg.dev/my-project/my-repo"
	)

	testCases := []struct {
		name     string
		provider *WorkloadIdentityFederationProvider
		credType credentials.Type
		repoURL  string
		assert   func(t *testing.T, result bool)
	}{
		{
			name:     "supports image credentials for GAR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			assert: func(t *testing.T, result bool) {
				assert.True(t, result)
			},
		},
		{
			name:     "supports image credentials for GCR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, result bool) {
				assert.True(t, result)
			},
		},
		{
			name:     "supports Helm credentials for GAR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeHelm,
			repoURL:  fakeGARRepoURL,
			assert: func(t *testing.T, result bool) {
				assert.True(t, result)
			},
		},
		{
			name:     "supports Helm credentials for GCR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeHelm,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, result bool) {
				assert.True(t, result)
			},
		},
		{
			name:     "rejects unsupported credential type",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeGit,
			repoURL:  fakeGARRepoURL,
			assert: func(t *testing.T, result bool) {
				assert.False(t, result)
			},
		},
		{
			name:     "rejects non-GAR/GCR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeImage,
			repoURL:  "docker.io/library/alpine",
			assert: func(t *testing.T, result bool) {
				assert.False(t, result)
			},
		},
		{
			name:     "rejects Helm credentials for non-GAR/GCR URL",
			provider: &WorkloadIdentityFederationProvider{projectID: fakeProjectID},
			credType: credentials.TypeHelm,
			repoURL:  "docker.io/library/alpine",
			assert: func(t *testing.T, result bool) {
				assert.False(t, result)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			supports, err := testCase.provider.Supports(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
				},
			)
			require.NoError(t, err)
			testCase.assert(t, supports)
		})
	}
}

func TestWorkloadIdentityFederationProvider_GetCredentials(t *testing.T) {
	t.Parallel()

	const (
		fakeProjectID  = "test-project"
		fakeProject    = "kargo-project"
		fakeGCRRepoURL = "gcr.io/my-project/my-repo"
		fakeToken      = "fake-token"
	)

	testCases := []struct {
		name                  string
		provider              *WorkloadIdentityFederationProvider
		setupTokenCache       func(c *cache.Cache)
		setupTokenSourceCache func(c *cache.Cache)
		project               string
		credType              credentials.Type
		repoURL               string
		assert                func(
			t *testing.T,
			tokenCache *cache.Cache,
			tokenSourceCache *cache.Cache,
			creds *credentials.Credentials,
			err error,
		)
	}{
		{
			name: "token cache hit",
			provider: &WorkloadIdentityFederationProvider{
				projectID:  fakeProjectID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
			},
			setupTokenCache: func(c *cache.Cache) {
				c.Set(tokenCacheKey(fakeProject), fakeToken, cache.DefaultExpiration)
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, _ *cache.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeToken, creds.Password)
			},
		},
		{
			name: "token cache miss, token source cache hit",
			provider: &WorkloadIdentityFederationProvider{
				projectID:        fakeProjectID,
				tokenCache:       cache.New(10*time.Hour, time.Hour),
				tokenSourceCache: cache.New(10*time.Hour, time.Hour),
				getAccessTokenFn: func(context.Context, string) (string, time.Time, error) {
					return fakeToken, time.Now().Add(time.Hour), nil
				},
			},
			setupTokenSourceCache: func(c *cache.Cache) {
				c.Set(tokenCacheKey(fakeProject), newFakeTokenSource(fakeToken), cache.DefaultExpiration)
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, _ *cache.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeToken, creds.Password)
			},
		},
		{
			name: "miss in both caches, successful token fetch",
			provider: &WorkloadIdentityFederationProvider{
				projectID:        fakeProjectID,
				tokenCache:       cache.New(10*time.Hour, time.Hour),
				tokenSourceCache: cache.New(10*time.Hour, time.Hour),
				getAccessTokenFn: func(context.Context, string) (string, time.Time, error) {
					return fakeToken, time.Now().Add(time.Hour), nil
				},
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, tokenCache, _ *cache.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeToken, creds.Password)

				// Verify the token was cached with a TTL based on the token's actual expiry
				items := tokenCache.Items()
				item, found := items[tokenCacheKey(fakeProject)]
				assert.True(t, found)
				expectedTTL := 55 * time.Minute // 1h expiry - 5m margin
				actualTTL := time.Until(time.Unix(0, item.Expiration))
				assert.InDelta(t, expectedTTL.Seconds(), actualTTL.Seconds(), 5)
			},
		},
		{
			name: "error in getAccessToken",
			provider: &WorkloadIdentityFederationProvider{
				projectID:        fakeProjectID,
				tokenCache:       cache.New(10*time.Hour, time.Hour),
				tokenSourceCache: cache.New(10*time.Hour, time.Hour),
				getAccessTokenFn: func(context.Context, string) (string, time.Time, error) {
					return "", time.Time{}, fmt.Errorf("token fetch error")
				},
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, _ *cache.Cache, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "token fetch error")
				assert.Nil(t, creds)
			},
		},
		{
			name: "empty token from getAccessToken falls back to default token source",
			provider: &WorkloadIdentityFederationProvider{
				projectID:        fakeProjectID,
				tokenCache:       cache.New(10*time.Hour, time.Hour),
				tokenSourceCache: cache.New(10*time.Hour, time.Hour),
				getAccessTokenFn: func(context.Context, string) (string, time.Time, error) {
					return "", time.Time{}, nil
				},
				tokenSource: newFakeTokenSource(fakeToken),
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, tokenSourceCache *cache.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeToken, creds.Password)

				// Verify the token source was cached
				tokenSource, found := tokenSourceCache.Get(tokenCacheKey(fakeProject))
				assert.True(t, found)
				ts, ok := tokenSource.(oauth2.TokenSource)
				assert.True(t, ok)
				token, err := ts.Token()
				assert.NoError(t, err)
				assert.Equal(t, fakeToken, token.AccessToken)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if testCase.setupTokenCache != nil {
				testCase.setupTokenCache(testCase.provider.tokenCache)
			}
			if testCase.setupTokenSourceCache != nil {
				testCase.setupTokenSourceCache(testCase.provider.tokenSourceCache)
			}
			creds, err := testCase.provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					Project: testCase.project,
					RepoURL: testCase.repoURL,
				},
			)
			testCase.assert(
				t,
				testCase.provider.tokenCache,
				testCase.provider.tokenSourceCache,
				creds,
				err,
			)
		})
	}
}

type fakeTokenSource struct {
	token string
}

func newFakeTokenSource(token string) oauth2.TokenSource {
	return &fakeTokenSource{token: token}
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: f.token}, nil
}

func TestWorkloadIdentityFederationProvider_GetCredentials_coalescing(
	t *testing.T,
) {
	const (
		fakeProjectID   = "test-project"
		fakeProject     = "fake-project"
		fakeRepoURL     = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeAccessToken = "fake-access-token"

		concurrency = 10
	)

	var acquisitions atomic.Int32

	provider := &WorkloadIdentityFederationProvider{
		projectID:        fakeProjectID,
		tokenCache:       cache.New(40*time.Minute, time.Hour),
		tokenSourceCache: cache.New(12*time.Hour, time.Hour),
		getAccessTokenFn: func(
			context.Context,
			string,
		) (string, time.Time, error) {
			acquisitions.Add(1)
			// Hold the acquisition open long enough for the other goroutines to
			// pile up behind it.
			time.Sleep(50 * time.Millisecond)
			return fakeAccessToken, time.Now().Add(time.Hour), nil
		},
	}

	creds := make([]*credentials.Credentials, concurrency)
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Go(func() {
			creds[i], errs[i] = provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:    credentials.TypeImage,
					Project: fakeProject,
					RepoURL: fakeRepoURL,
				},
			)
		})
	}
	wg.Wait()

	// However the goroutines were scheduled, exactly one token should have been
	// obtained. Callers that missed the in-flight acquisition entirely find the
	// token already cached when their own flight begins.
	require.Equal(t, int32(1), acquisitions.Load())
	for i := range concurrency {
		require.NoError(t, errs[i])
		require.NotNil(t, creds[i])
		require.Equal(t, accessTokenUsername, creds[i].Username)
		require.Equal(t, fakeAccessToken, creds[i].Password)
	}
}

// See the comment on TestServiceAccountKeyProvider_GetCredentials_winnerCanceled.
func TestWorkloadIdentityFederationProvider_GetCredentials_winnerCanceled(
	t *testing.T,
) {
	const (
		fakeProjectID   = "test-project"
		fakeProject     = "fake-project"
		fakeRepoURL     = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeAccessToken = "fake-access-token"
	)

	var acquisitions atomic.Int32
	acquisitionStarted := make(chan struct{})
	release := make(chan struct{})

	provider := &WorkloadIdentityFederationProvider{
		projectID:        fakeProjectID,
		tokenCache:       cache.New(40*time.Minute, time.Hour),
		tokenSourceCache: cache.New(12*time.Hour, time.Hour),
		getAccessTokenFn: func(
			ctx context.Context,
			_ string,
		) (string, time.Time, error) {
			if acquisitions.Add(1) == 1 {
				close(acquisitionStarted)
			}
			select {
			case <-ctx.Done():
				return "", time.Time{}, ctx.Err()
			case <-release:
				return fakeAccessToken, time.Now().Add(time.Hour), nil
			}
		},
	}

	req := credentials.Request{
		Type:    credentials.TypeImage,
		Project: fakeProject,
		RepoURL: fakeRepoURL,
	}

	winnerCtx, cancel := context.WithCancel(t.Context())
	winnerErr := make(chan error, 1)
	go func() {
		_, err := provider.GetCredentials(winnerCtx, req)
		winnerErr <- err
	}()
	<-acquisitionStarted

	const numWaiters = 3
	type result struct {
		creds *credentials.Credentials
		err   error
	}
	waiterRes := make(chan result, numWaiters)
	for range numWaiters {
		go func() {
			creds, err := provider.GetCredentials(t.Context(), req)
			waiterRes <- result{creds: creds, err: err}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	cancel()

	winErr := <-winnerErr
	require.ErrorIs(t, winErr, context.Canceled)
	require.ErrorContains(t, winErr, "cache refresh will continue in the background")

	close(release)

	for range numWaiters {
		res := <-waiterRes
		require.NoError(t, res.err)
		require.NotNil(t, res.creds)
		require.Equal(t, fakeAccessToken, res.creds.Password)
	}

	require.Equal(t, int32(1), acquisitions.Load())

	cached, found := provider.tokenCache.Get(tokenCacheKey(fakeProject))
	require.True(t, found)
	require.Equal(t, fakeAccessToken, cached)
}
