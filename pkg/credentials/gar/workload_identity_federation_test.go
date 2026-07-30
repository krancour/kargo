package gar

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/akuity/kargo/pkg/cache/expiring"
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
		setupTokenCache       func(c expiring.Cache)
		setupTokenSourceCache func(c expiring.Cache)
		project               string
		credType              credentials.Type
		repoURL               string
		assert                func(
			t *testing.T,
			tokenCache expiring.Cache,
			tokenSourceCache expiring.Cache,
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
			setupTokenCache: func(c expiring.Cache) {
				c.Set(tokenCacheKey(fakeProject), fakeToken, cache.DefaultExpiration)
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, _ expiring.Cache, creds *credentials.Credentials, err error) {
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
				// A token distinct from the cached token source's, so that skipping
				// the token source cache and acquiring one instead is detectable.
				getAccessTokenFn: func(context.Context, string) (string, time.Time, error) {
					return "token-from-an-acquisition", time.Now().Add(time.Hour), nil
				},
			},
			setupTokenSourceCache: func(c expiring.Cache) {
				c.Set(tokenCacheKey(fakeProject), newFakeTokenSource(fakeToken), cache.DefaultExpiration)
			},
			project:  fakeProject,
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			assert: func(t *testing.T, _, _ expiring.Cache, creds *credentials.Credentials, err error) {
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
			assert: func(t *testing.T, tokenCache, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeToken, creds.Password)

				// Verify the token was cached with a TTL based on the token's actual expiry
				realCache, ok := tokenCache.(*cache.Cache)
				require.True(t, ok)
				items := realCache.Items()
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
			assert: func(t *testing.T, _, _ expiring.Cache, creds *credentials.Credentials, err error) {
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
			assert: func(t *testing.T, _, tokenSourceCache expiring.Cache, creds *credentials.Credentials, err error) {
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
		fakeRepoURL = "us-central1-docker.pkg.dev/my-project/my-repo"

		concurrency = 10
	)

	// Callers are split evenly between two Kargo Projects. Since the cache key is
	// derived from the Project, the two groups must not share an acquisition,
	// which is what makes this test sensitive to an acquisition keyed on anything
	// coarser than the cache key.
	projects := []string{"fake-project-a", "fake-project-b"}

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when every caller
	// is waiting on the acquisition, rather than guess at it with a sleep.
	synctest.Test(t, func(t *testing.T) {
		// Track the total number of actual invocations of the acquisition function.
		// If the coalescing is working, there should only ever be one per Project
		// regardless of how many concurrent callers there are.
		var acquisitions atomic.Int32

		// This channel will be used to unblock the goroutines that are parked
		// inside the acquisition function, but only after every caller is durably
		// blocked on a singleflight.
		release := make(chan struct{})

		provider := &WorkloadIdentityFederationProvider{
			// Caches that retain nothing leave the acquisition as the only way any
			// caller can obtain a token.
			tokenCache:       expiring.NewAlwaysMissing(),
			tokenSourceCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(
				_ context.Context,
				project string,
			) (string, time.Time, error) {
				acquisitions.Add(1)
				<-release
				// The token identifies the Project it was obtained for, so a caller
				// served by another Project's acquisition is detectable.
				return "token-for-" + project, time.Now().Add(time.Hour), nil
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
						Project: projects[i%len(projects)],
						RepoURL: fakeRepoURL,
					},
				)
			})
		}

		// Returns only once every caller is durably blocked. Neither cache cannot have
		// served any of them, so the only place they can be is waiting on an
		// acquisition. Specifically, singleflight has started one goroutine per
		// Project and each is parked inside the acquisition function, with every
		// caller waiting on the flight it joined.
		synctest.Wait()

		// Allows acquisitions to complete.
		close(release)

		// Wait for all of the callers to finish.
		wg.Wait()

		// Since we forced the callers for each Project to join one singleflight,
		// there should have been exactly one actual execution of the acquisition
		// function per Project -- no more, which would mean callers failed to
		// coalesce, and no fewer, which would mean callers for different Projects
		// shared an acquisition.
		require.Equal(t, int32(len(projects)), acquisitions.Load())

		// Verify that every caller received the credentials for its own Project and
		// no errors.
		for i := range concurrency {
			require.NoError(t, errs[i])
			require.NotNil(t, creds[i])
			require.Equal(t, accessTokenUsername, creds[i].Username)
			require.Equal(
				t, "token-for-"+projects[i%len(projects)], creds[i].Password,
			)
		}
	})
}

func TestWorkloadIdentityFederationProvider_GetCredentials_winnerCanceled(
	t *testing.T,
) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context
	// detached from any caller's, it must survive that and still serve the
	// remaining callers waiting on it.

	const (
		fakeProject     = "fake-project"
		fakeRepoURL     = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeAccessToken = "fake-access-token"

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

		provider := &WorkloadIdentityFederationProvider{
			// Caches that retain nothing leave the acquisition as the only way any
			// caller can obtain a token.
			tokenCache:       expiring.NewAlwaysMissing(),
			tokenSourceCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(
				ctx context.Context,
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
					return fakeAccessToken, time.Now().Add(time.Hour), nil
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

		// Returns once the waiters have joined the winner's flight, leaving the
		// goroutine singleflight started parked inside the acquisition function and
		// every caller waiting on that flight.
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
			require.Equal(t, accessTokenUsername, res.creds.Username)
			require.Equal(t, fakeAccessToken, res.creds.Password)
		}
	})
}

func TestWorkloadIdentityFederationProvider_GetCredentials_acquisitionTimeout(
	t *testing.T,
) {
	// An acquisition runs under a context detached from every caller's, so a
	// caller giving up cannot end it. Its own deadline is the only thing that
	// can, and until it does, the singleflight key stays occupied and no fresh
	// acquisition for that key can begin.

	const (
		fakeProject = "fake-project"
		fakeRepoURL = "us-central1-docker.pkg.dev/my-project/my-repo"
	)

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. A bubble has a fake
	// clock that advances only when every goroutine in the group is durably
	// blocked, and then only as far as the next pending timer. Here that timer is
	// the acquisition's own deadline, so this test reaches it instantly and
	// measures it exactly, instead of waiting 30 seconds for it.
	synctest.Test(t, func(t *testing.T) {
		provider := &WorkloadIdentityFederationProvider{
			tokenCache:       expiring.NewAlwaysMissing(),
			tokenSourceCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(
				ctx context.Context,
				_ string,
			) (string, time.Time, error) {
				// Never resolves on its own, so only the acquisition's deadline can
				// end it.
				<-ctx.Done()
				return "", time.Time{}, ctx.Err()
			},
		}

		start := time.Now()
		creds, err := provider.GetCredentials(
			t.Context(),
			credentials.Request{
				Type:    credentials.TypeImage,
				Project: fakeProject,
				RepoURL: fakeRepoURL,
			},
		)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Nil(t, creds)
		require.Equal(t, tokenAcquisitionTimeout, time.Since(start))
	})
}
