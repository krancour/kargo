package gar

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
	"golang.org/x/oauth2"

	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
)

func TestNewServiceAccountKeyProvider(t *testing.T) {
	provider := NewServiceAccountKeyProvider().(*ServiceAccountKeyProvider) // nolint:forcetypeassert
	assert.NotNil(t, provider)

	assert.NotNil(t, provider.tokenCache)
	assert.NotNil(t, provider.getAccessTokenFn)
}

func TestServiceAccountKeyProvider_Supports(t *testing.T) {
	const (
		fakeGCRRepoURL        = "gcr.io/my-project/my-repo"
		fakeGARRepoURL        = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeServiceAccountKey = "base64-encoded-service-account-key"
	)

	testCases := []struct {
		name     string
		credType credentials.Type
		repoURL  string
		data     map[string][]byte
		expected bool
	}{
		{
			name:     "valid GCR image repo with service account key",
			credType: credentials.TypeImage,
			repoURL:  fakeGCRRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: true,
		},
		{
			name:     "valid GAR image repo with service account key",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: true,
		},
		{
			name:     "unsupported credential type",
			credType: credentials.TypeGit,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: false,
		},
		{
			name:     "missing service account key",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data:     map[string][]byte{},
			expected: false,
		},
		{
			name:     "nil data",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data:     nil,
			expected: false,
		},
		{
			name:     "non-GAR/GCR URL",
			credType: credentials.TypeImage,
			repoURL:  "docker.io/library/nginx",
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: false,
		},
		// Helm chart test cases
		{
			name:     "valid GAR chart repo with service account key",
			credType: credentials.TypeHelm,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: true,
		},
		{
			name:     "valid GCR chart repo with service account key",
			credType: credentials.TypeHelm,
			repoURL:  fakeGCRRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: true,
		},
		{
			name:     "Helm chart repo with non-GAR/GCR URL",
			credType: credentials.TypeHelm,
			repoURL:  "docker.io/library/nginx",
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			expected: false,
		},
		{
			name:     "Helm chart repo missing service account key",
			credType: credentials.TypeHelm,
			repoURL:  fakeGARRepoURL,
			data:     map[string][]byte{},
			expected: false,
		},
	}

	p := NewServiceAccountKeyProvider()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			supports, err := p.Supports(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
					Data:    testCase.data,
				},
			)
			require.NoError(t, err)
			require.Equal(t, testCase.expected, supports)
		})
	}
}

func TestServiceAccountKeyProvider_GetCredentials(t *testing.T) {
	const (
		fakeGARRepoURL        = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeServiceAccountKey = "base64-encoded-service-account-key"
		fakeAccessToken       = "fake-access-token"
	)

	testCases := []struct {
		name             string
		credType         credentials.Type
		repoURL          string
		data             map[string][]byte
		getAccessTokenFn func(
			ctx context.Context,
			encodedServiceAccountKey string,
		) (*oauth2.Token, error)
		setupCache func(c expiring.Cache)
		assertions func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error)
	}{
		{
			name:     "cache hit",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			setupCache: func(c expiring.Cache) {
				cacheKey := tokenCacheKey(fakeServiceAccountKey)
				c.Set(cacheKey, fakeAccessToken, cache.DefaultExpiration)
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeAccessToken, creds.Password)
			},
		},
		{
			name:     "cache miss, successful token fetch",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			getAccessTokenFn: func(context.Context, string) (*oauth2.Token, error) {
				return &oauth2.Token{
					AccessToken: fakeAccessToken,
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
			assertions: func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeAccessToken, creds.Password)

				// Verify the token was cached with a TTL based on the
				// token's actual expiry
				realCache, ok := c.(*cache.Cache)
				require.True(t, ok)
				items := realCache.Items()
				item, found := items[tokenCacheKey(fakeServiceAccountKey)]
				assert.True(t, found)
				expectedTTL := 55 * time.Minute // 1h expiry - 5m margin
				actualTTL := time.Until(time.Unix(0, item.Expiration))
				assert.InDelta(t, expectedTTL.Seconds(), actualTTL.Seconds(), 5)
			},
		},
		{
			name:     "error in getAccessToken",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			getAccessTokenFn: func(context.Context, string) (*oauth2.Token, error) {
				return nil, errors.New("access token error")
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "error getting GCP access token")
				assert.Nil(t, creds)
			},
		},
		{
			name:     "empty token from getAccessToken",
			credType: credentials.TypeImage,
			repoURL:  fakeGARRepoURL,
			data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
			getAccessTokenFn: func(context.Context, string) (*oauth2.Token, error) {
				return nil, nil
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := NewServiceAccountKeyProvider().(*ServiceAccountKeyProvider) // nolint:forcetypeassert
			provider.getAccessTokenFn = testCase.getAccessTokenFn

			if testCase.setupCache != nil {
				testCase.setupCache(provider.tokenCache)
			}

			creds, err := provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
					Data:    testCase.data,
				},
			)
			testCase.assertions(t, provider.tokenCache, creds, err)
		})
	}
}

func TestServiceAccountKeyProvider_GetCredentials_coalescing(t *testing.T) {
	const (
		fakeRepoURL           = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeServiceAccountKey = "fake-service-account-key"
		fakeAccessToken       = "fake-access-token"

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

		provider := &ServiceAccountKeyProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(context.Context, string) (*oauth2.Token, error) {
				acquisitions.Add(1)
				<-release
				return &oauth2.Token{
					AccessToken: fakeAccessToken,
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
		}

		req := credentials.Request{
			Type:    credentials.TypeImage,
			RepoURL: fakeRepoURL,
			Data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
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
			require.Equal(t, accessTokenUsername, creds[i].Username)
			require.Equal(t, fakeAccessToken, creds[i].Password)
		}
	})
}

func TestServiceAccountKeyProvider_GetCredentials_winnerCanceled(t *testing.T) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context
	// detached from any caller's, it must survive that and still serve the
	// remaining callers waiting on it.

	const (
		fakeRepoURL           = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeServiceAccountKey = "fake-service-account-key"
		fakeAccessToken       = "fake-access-token"

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

		provider := &ServiceAccountKeyProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(
				ctx context.Context,
				_ string,
			) (*oauth2.Token, error) {
				// Honoring the context is what gives this test its teeth. Were the
				// acquisition running under the winner's context instead of a
				// detached one, canceling the winner would land in the first case
				// below and every waiter would receive that error.
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return &oauth2.Token{
						AccessToken: fakeAccessToken,
						Expiry:      time.Now().Add(time.Hour),
					}, nil
				}
			},
		}

		req := credentials.Request{
			Type:    credentials.TypeImage,
			RepoURL: fakeRepoURL,
			Data: map[string][]byte{
				serviceAccountKeyKey: []byte(fakeServiceAccountKey),
			},
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
			require.Equal(t, accessTokenUsername, res.creds.Username)
			require.Equal(t, fakeAccessToken, res.creds.Password)
		}
	})
}
