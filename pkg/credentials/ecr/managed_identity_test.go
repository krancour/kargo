package ecr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when every caller
	// is waiting on the acquisition, rather than guess at it with a sleep.
	synctest.Test(t, func(t *testing.T) {
		// Track the total number of actual invocations of the acquisition function.
		// If the coalescing is working, there should only ever be one regardless of
		// how many concurrent callers there are.
		var acquisitions atomic.Int32

		// This channel will be used to unblock the goroutine that is parked inside
		// the acquisition function, but only after every caller is durably blocked
		// on the singleflight.
		release := make(chan struct{})

		provider := &ManagedIdentityProvider{
			accountID: fakeAccountID,
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAuthTokenFn: func(
				context.Context,
				string,
				string,
			) (string, time.Time, error) {
				acquisitions.Add(1)
				<-release
				return fakeToken, time.Now().Add(12 * time.Hour), nil
			},
		}

		req := credentials.Request{
			Type:    credentials.TypeImage,
			Project: fakeProject,
			RepoURL: fakeRepoURL,
		}

		creds := make([]*credentials.Credentials, concurrency)
		errs := make([]error, concurrency)
		var wg sync.WaitGroup
		for i := range concurrency {
			wg.Go(func() {
				creds[i], errs[i] = provider.GetCredentials(t.Context(), req)
			})
		}

		// Returns only once every caller is durably blocked. The cache cannot have
		// served any of them, so the only place they can be is waiting on the one
		// acquisition. Specifically, one goroutine should be parked inside the
		// acquisition function with the rest waiting on the singleflight to
		// complete.
		synctest.Wait()

		// Allows acquisition to complete.
		close(release)

		// Wait for all of the callers to finish.
		wg.Wait()

		// Since we forced every caller to join one singleflight, there should have
		// been only one actual execution of the acquisition function.
		require.Equal(t, int32(1), acquisitions.Load())

		// Verify that every caller received the expected credentials and no
		// errors.
		for i := range concurrency {
			require.NoError(t, errs[i])
			require.NotNil(t, creds[i])
			require.Equal(t, "AWS", creds[i].Username)
			require.Equal(t, "password", creds[i].Password)
		}
	})
}

func TestManagedIdentityProvider_GetCredentials_winnerCanceled(t *testing.T) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context
	// detached from any caller's, it must survive that and still serve the
	// remaining callers waiting on it.

	const (
		fakeAccountID = "123456789012"
		fakeProject   = "fake-project"
		fakeRepoURL   = "123456789012.dkr.ecr.us-west-2.amazonaws.com/repo"
		// base64 of "AWS:password"
		fakeToken = "QVdTOnBhc3N3b3Jk" // nolint:gosec

		numWaiters = 3
	)

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when the first
	// caller begins blocking on the acquisition so that waiting callers can be
	// launched. This phased launch sequence allows us to know with certainty
	// which goroutine is the winning caller and which are the waiters. Likewise,
	// synctest.Wait() allows the test to observe the moment when all of the
	// waiting callers are blocking so that we can proceed with canceling the
	// winner's context.
	synctest.Test(t, func(t *testing.T) {
		// This channel will be used to unblock the goroutine that is parked inside
		// the acquisition function, but only after the winner has abandoned it.
		release := make(chan struct{})

		provider := &ManagedIdentityProvider{
			accountID: fakeAccountID,
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAuthTokenFn: func(
				ctx context.Context,
				_ string,
				_ string,
			) (string, time.Time, error) {
				// Honoring the context is what gives this test its teeth. Were the
				// acquisition running under the winner's context instead of a
				// detached one, canceling the winner would land in the first case
				// below and every waiter would receive that error.
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

		// The winner is launched alone, and with a context only it holds.
		winnerCtx, cancel := context.WithCancel(t.Context())
		winnerErr := make(chan error, 1)
		go func() {
			_, err := provider.GetCredentials(winnerCtx, req)
			winnerErr <- err
		}()

		// Returns once the winner and the goroutine singleflight started on its
		// behalf are both durably blocked: the latter parked inside the acquisition
		// function, the former waiting on it. The flight therefore exists and
		// belongs to the winner. Launching the waiters any earlier would leave
		// ownership of the flight to the scheduler, and canceling a caller that
		// does not own it proves nothing.
		synctest.Wait()

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

		// Returns once the waiters have joined the winner's flight, leaving one
		// goroutine parked inside the acquisition function and everyone else
		// waiting on the singleflight to complete.
		synctest.Wait()

		// The winner abandons the flight. The waiters' contexts stay live, so they
		// keep waiting.
		cancel()

		// The winner reports that it stopped waiting, rather than reporting a
		// failed acquisition.
		winErr := <-winnerErr
		require.ErrorIs(t, winErr, context.Canceled)
		require.ErrorContains(t, winErr, "cache refresh will continue in the background")

		// Because of the acquisition's orphaned context, the winner's cancellation
		// should not have halted it. Closing the release channel unblocks the
		// acquisition. Doing so before collecting the waiters' results below
		// matters. If every goroutine in the bubble were parked, synctest.Test()'s
		// fake clock would advance to the acquisition's own deadline and fail it
		// for reasons having nothing to do with what is under test.
		close(release)

		// Verify that every waiter received the expected credentials and no
		// errors, meaning the acquisition outlived the caller that started it.
		for range numWaiters {
			res := <-waiterRes
			require.NoError(t, res.err)
			require.NotNil(t, res.creds)
			require.Equal(t, "AWS", res.creds.Username)
			require.Equal(t, "password", res.creds.Password)
		}
	})
}
