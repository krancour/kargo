package ecr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
)

func TestManagedIdentityProvider_Supports(t *testing.T) {
	const (
		fakeAccountID = "123456789012"
		fakeRepoURL   = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
	)

	testCases := []struct {
		name     string
		provider *ManagedIdentityProvider
		credType credentials.Type
		repoURL  string
		expected bool
	}{
		{
			name: "no account ID configured",
			provider: &ManagedIdentityProvider{
				accountID: "",
			},
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			expected: false,
		},
		{
			name: "image credentials supported",
			provider: &ManagedIdentityProvider{
				accountID: fakeAccountID,
			},
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			expected: true,
		},
		{
			name: "helm credentials supported",
			provider: &ManagedIdentityProvider{
				accountID: fakeAccountID,
			},
			credType: credentials.TypeHelm,
			repoURL:  fakeRepoURL,
			expected: true,
		},
		{
			name: "git credentials not supported",
			provider: &ManagedIdentityProvider{
				accountID: fakeAccountID,
			},
			credType: credentials.TypeGit,
			repoURL:  fakeRepoURL,
			expected: false,
		},
		{
			name: "non-ECR image URL not supported",
			provider: &ManagedIdentityProvider{
				accountID: fakeAccountID,
			},
			credType: credentials.TypeImage,
			repoURL:  "us-docker.pkg.dev/project/repo/image",
			expected: false,
		},
		{
			name: "non-ECR helm URL not supported",
			provider: &ManagedIdentityProvider{
				accountID: fakeAccountID,
			},
			credType: credentials.TypeHelm,
			repoURL:  "us-docker.pkg.dev/project/repo/chart",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			supports, err := testCase.provider.Supports(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
				},
			)
			require.NoError(t, err)
			require.Equal(t, testCase.expected, supports)
		})
	}
}

func TestManagedIdentityProvider_GetCredentials(t *testing.T) {
	const (
		fakeAccountID = "123456789012"
		fakeProject   = "fake-project"
		fakeRepoURL   = "123456789012.dkr.ecr.us-west-2.amazonaws.com/repo"
		fakeRegion    = "us-west-2"
		// base64 of "AWS:password"
		fakeToken = "QVdTOnBhc3N3b3Jk" // nolint:gosec
	)

	testCases := []struct {
		name       string
		provider   *ManagedIdentityProvider
		project    string
		credType   credentials.Type
		repoURL    string
		setupCache func(cache expiring.Cache)
		assertions func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error)
	}{
		{
			name: "not supported",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
			},
			project:  fakeProject,
			credType: credentials.TypeGit,
			repoURL:  "git://repo",
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)
			},
		},
		{
			name: "non-ECR URL",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  "not-an-ecr-url",
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)
			},
		},
		{
			name: "cache hit",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			setupCache: func(c expiring.Cache) {
				cacheKey := tokenCacheKey(fakeRegion, fakeProject)
				c.Set(cacheKey, fakeToken, cache.DefaultExpiration)
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, "AWS", creds.Username)
				assert.Equal(t, "password", creds.Password)
			},
		},
		{
			name: "cache miss, successful token fetch",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
				getAuthTokenFn: func(
					context.Context,
					string,
					string,
				) (string, time.Time, error) {
					return fakeToken, time.Now().Add(12 * time.Hour), nil
				},
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			assertions: func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, "AWS", creds.Username)
				assert.Equal(t, "password", creds.Password)

				// Verify the token was cached with a TTL based on the
				// token's actual expiry
				realCache, ok := c.(*cache.Cache)
				require.True(t, ok)
				items := realCache.Items()
				item, found := items[tokenCacheKey(fakeRegion, fakeProject)]
				assert.True(t, found)
				expectedTTL := 12*time.Hour - 5*time.Minute // 12h expiry - 5m margin
				actualTTL := time.Until(time.Unix(0, item.Expiration))
				assert.InDelta(t, expectedTTL.Seconds(), actualTTL.Seconds(), 5)
			},
		},
		{
			name: "error in getAuthToken",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
				getAuthTokenFn: func(
					context.Context,
					string,
					string,
				) (string, time.Time, error) {
					return "", time.Time{}, errors.New("auth token error")
				},
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "error getting ECR auth token")
				assert.Nil(t, creds)
			},
		},
		{
			name: "empty token from getAuthToken",
			provider: &ManagedIdentityProvider{
				accountID:  fakeAccountID,
				tokenCache: cache.New(10*time.Hour, time.Hour),
				getAuthTokenFn: func(
					context.Context,
					string,
					string,
				) (string, time.Time, error) {
					return "", time.Time{}, nil
				},
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.setupCache != nil {
				testCase.setupCache(testCase.provider.tokenCache)
			}
			creds, err := testCase.provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					Project: testCase.project,
					RepoURL: testCase.repoURL,
				},
			)
			testCase.assertions(t, testCase.provider.tokenCache, creds, err)
		})
	}
}

func TestManagedIdentityProvider_GetCredentials_coalescing(t *testing.T) {
	const (
		fakeAccountID = "123456789012"
		fakeProject   = "fake-project"
		fakeRepoURL   = "123456789012.dkr.ecr.us-west-2.amazonaws.com/repo"
		// base64 of "AWS:password"
		fakeToken = "QVdTOnBhc3N3b3Jk" // nolint:gosec

		concurrency = 10
	)

	var acquisitions atomic.Int32

	provider := &ManagedIdentityProvider{
		accountID:  fakeAccountID,
		tokenCache: cache.New(10*time.Hour, time.Hour),
		getAuthTokenFn: func(
			context.Context,
			string,
			string,
		) (string, time.Time, error) {
			acquisitions.Add(1)
			// Hold the acquisition open long enough for the other goroutines to
			// pile up behind it.
			time.Sleep(50 * time.Millisecond)
			return fakeToken, time.Now().Add(12 * time.Hour), nil
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
		require.Equal(t, "AWS", creds[i].Username)
		require.Equal(t, "password", creds[i].Password)
	}
}

// This exercises the case where the context of the call that wins the
// singleflight race is canceled mid-acquisition. Because the acquisition runs
// under a detached context, the winner's cancellation must NOT cancel the
// shared acquisition. The winner stops waiting and returns an "interrupted"
// error, but the single acquisition runs to completion and the coalesced
// callers whose contexts are still live receive the token from that SAME
// acquisition.
func TestManagedIdentityProvider_GetCredentials_winnerCanceled(t *testing.T) {
	const (
		fakeAccountID = "123456789012"
		fakeProject   = "fake-project"
		fakeRepoURL   = "123456789012.dkr.ecr.us-west-2.amazonaws.com/repo"
		fakeRegion    = "us-west-2"
		// base64 of "AWS:password"
		fakeToken = "QVdTOnBhc3N3b3Jk" // nolint:gosec
	)

	var acquisitions atomic.Int32
	acquisitionStarted := make(chan struct{})
	release := make(chan struct{})

	provider := &ManagedIdentityProvider{
		accountID:  fakeAccountID,
		tokenCache: cache.New(10*time.Hour, time.Hour),
		getAuthTokenFn: func(
			ctx context.Context,
			_ string,
			_ string,
		) (string, time.Time, error) {
			if acquisitions.Add(1) == 1 {
				close(acquisitionStarted)
			}
			// Hold the acquisition open until the test releases it. Were the
			// acquisition's context derived from the winner's, this would instead
			// return early with the winner's cancellation.
			select {
			case <-ctx.Done():
				return "", time.Time{}, ctx.Err()
			case <-release:
				return fakeToken, time.Now().Add(12 * time.Hour), nil
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
	// The winner is now mid-acquisition with its flight held open
	<-acquisitionStarted

	// Several coalesced waiters, all with live contexts, all of which should
	// recover from the winner's cancellation.
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

	// Give the waiters a moment to join the in-flight acquisition, then cancel
	// the winner's context.
	time.Sleep(100 * time.Millisecond)
	cancel()

	// The winner's own context was canceled, so it stops waiting and returns an
	// interrupted error -- but it must NOT have canceled the shared acquisition.
	winErr := <-winnerErr
	require.ErrorIs(t, winErr, context.Canceled)
	require.ErrorContains(t, winErr, "cache refresh will continue in the background")

	// Allow the detached acquisition to complete.
	close(release)

	for range numWaiters {
		res := <-waiterRes
		require.NoError(t, res.err)
		require.NotNil(t, res.creds)
		require.Equal(t, "password", res.creds.Password)
	}

	// Exactly one acquisition: the winner's cancellation did not spawn a
	// replacement, and the single detached acquisition served every waiter.
	require.Equal(t, int32(1), acquisitions.Load())

	// That acquisition also cached the token for future callers.
	cached, found := provider.tokenCache.Get(tokenCacheKey(fakeRegion, fakeProject))
	require.True(t, found)
	require.Equal(t, fakeToken, cached)
}
