package ecr

import (
	"context"
	"encoding/base64"
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

func TestNewAccessKeyProvider(t *testing.T) {
	provider := NewAccessKeyProvider().(*AccessKeyProvider) // nolint:forcetypeassert

	assert.NotNil(t, provider)
	assert.NotNil(t, provider.tokenCache)
	assert.NotNil(t, provider.getAuthTokenFn)
}

func TestAccessKeyProvider_Supports(t *testing.T) {
	const (
		fakeRepoURL = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"

		fakeRegion = "us-west-2"
		fakeID     = "AKIAIOSFODNN7EXAMPLE"                     // nolint:gosec
		fakeSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // nolint:gosec
	)

	testCases := []struct {
		name     string
		credType credentials.Type
		repoURL  string
		data     map[string][]byte
		expected bool
	}{
		{
			name:     "valid image credentials",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: true,
		},
		{
			name:     "valid helm oci credentials",
			credType: credentials.TypeHelm,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: true,
		},
		{
			name:     "helm but not oci",
			credType: credentials.TypeHelm,
			repoURL:  "https://123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo",
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: false,
		},
		{
			name:     "missing region",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: false,
		},
		{
			name:     "missing access key ID",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				secretKey: []byte(fakeSecret),
			},
			expected: false,
		},
		{
			name:     "missing secret key",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
			},
			expected: false,
		},
		{
			name:     "invalid URL format",
			credType: credentials.TypeImage,
			repoURL:  "not-an-ecr-url",
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: false,
		},
		{
			name:     "unsupported credential type",
			credType: credentials.TypeGit,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			expected: false,
		},
		{
			name:     "empty data",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data:     map[string][]byte{},
			expected: false,
		},
	}

	p := NewAccessKeyProvider()

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

func TestAccessKeyProvider_GetCredentials(t *testing.T) {
	const (
		fakeRepoURL = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
		fakeRegion  = "us-west-2"
		fakeID      = "AKIAIOSFODNN7EXAMPLE"                     // nolint:gosec
		fakeSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // nolint:gosec
		// base64 of "AWS:password"
		fakeToken = "QVdTOnBhc3N3b3Jk" // nolint:gosec
	)

	testCases := []struct {
		name           string
		credType       credentials.Type
		repoURL        string
		data           map[string][]byte
		getAuthTokenFn func(
			ctx context.Context,
			region string,
			accessKeyID string,
			secretAccessKey string,
		) (string, time.Time, error)
		setupCache func(cache expiring.Cache)
		assertions func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error)
	}{
		{
			name:     "unsupported credentials",
			credType: credentials.TypeGit,
			repoURL:  "not-an-ecr-url",
			data:     map[string][]byte{},
			getAuthTokenFn: func(
				context.Context,
				string,
				string,
				string,
			) (string, time.Time, error) {
				return "", time.Time{}, nil
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)
			},
		},
		{
			name:     "cache hit",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			setupCache: func(c expiring.Cache) {
				c.Set(
					tokenCacheKey(fakeRegion, fakeID, fakeSecret),
					fakeToken, // base64 of "AWS:password"
					cache.DefaultExpiration,
				)
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, "AWS", creds.Username)
				assert.Equal(t, "password", creds.Password)
			},
		},
		{
			name:     "cache miss, successful token fetch",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			getAuthTokenFn: func(
				context.Context,
				string,
				string,
				string,
			) (string, time.Time, error) {
				return fakeToken, time.Now().Add(12 * time.Hour), nil
			},
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
				item, found := items[tokenCacheKey(fakeRegion, fakeID, fakeSecret)]
				assert.True(t, found)
				expectedTTL := 12*time.Hour - 5*time.Minute // 12h expiry - 5m margin
				actualTTL := time.Until(time.Unix(0, item.Expiration))
				assert.InDelta(t, expectedTTL.Seconds(), actualTTL.Seconds(), 5)
			},
		},
		{
			name:     "error in getAuthToken",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			getAuthTokenFn: func(
				context.Context,
				string,
				string,
				string,
			) (string, time.Time, error) {
				return "", time.Time{}, errors.New("auth token error")
			},
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "error getting ECR auth token")
				assert.Nil(t, creds)
			},
		},
		{
			name:     "empty token from getAuthToken",
			credType: credentials.TypeImage,
			repoURL:  fakeRepoURL,
			data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
			},
			getAuthTokenFn: func(
				context.Context,
				string,
				string,
				string,
			) (string, time.Time, error) {
				return "", time.Time{}, nil
			},
			assertions: func(t *testing.T, c expiring.Cache, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.NoError(t, err)

				// Verify the token was not cached
				_, found := c.Get(tokenCacheKey(fakeRegion, fakeID, fakeSecret))
				assert.False(t, found)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := NewAccessKeyProvider().(*AccessKeyProvider) // nolint:forcetypeassert
			provider.getAuthTokenFn = testCase.getAuthTokenFn

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

func Test_decodeAuthToken(t *testing.T) {
	testCases := []struct {
		name       string
		token      string
		assertions func(t *testing.T, creds *credentials.Credentials, err error)
	}{
		{
			name:  "valid token",
			token: "QVdTOnBhc3N3b3Jk", // base64 of "AWS:password"
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, "AWS", creds.Username)
				assert.Equal(t, "password", creds.Password)
			},
		},
		{
			name:  "invalid base64",
			token: "invalid-base64",
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "error decoding token")
				assert.Nil(t, creds)
			},
		},
		{
			name:  "valid base64 but invalid format",
			token: "bm90LWEtdmFsaWQtdG9rZW4=", // base64 of "not-a-valid-token"
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "invalid token format")
				assert.Nil(t, creds)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			creds, err := decodeAuthToken(testCase.token)
			testCase.assertions(t, creds, err)
		})
	}
}

func TestAccessKeyProvider_GetCredentials_coalescing(t *testing.T) {
	const (
		fakeRepoURL = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
		fakeRegion  = "us-west-2"
		fakeSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // nolint:gosec

		concurrency = 10
	)

	// Callers are split evenly between two sets of credentials. Since the cache
	// key includes the access key ID, the two groups must not share an
	// acquisition, which is what makes this test sensitive to an acquisition
	// keyed on anything coarser than the cache key.
	accessKeyIDs := []string{
		"AKIAIOSFODNN7EXAMPLE", // nolint:gosec
		"AKIAI44QH8DHBEXAMPLE", // nolint:gosec
	}

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when every caller
	// is waiting on the acquisition, rather than guess at it with a sleep.
	synctest.Test(t, func(t *testing.T) {
		// Track the total number of actual invocations of the acquisition function.
		// If the coalescing is working, there should only ever be one per access key
		// ID regardless of how many concurrent callers there are.
		var acquisitions atomic.Int32

		// This channel will be used to unblock the goroutines that are parked
		// inside the acquisition function, but only after every caller is durably
		// blocked on a singleflight.
		release := make(chan struct{})

		provider := &AccessKeyProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAuthTokenFn: func(
				_ context.Context,
				_ string,
				accessKeyID string,
				_ string,
			) (string, time.Time, error) {
				acquisitions.Add(1)
				<-release
				// The token identifies the credentials it was obtained with, so a
				// caller served by another set's acquisition is detectable.
				return base64.StdEncoding.EncodeToString(
					[]byte("AWS:" + accessKeyID),
				), time.Now().Add(12 * time.Hour), nil
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
						RepoURL: fakeRepoURL,
						Data: map[string][]byte{
							regionKey: []byte(fakeRegion),
							idKey:     []byte(accessKeyIDs[i%len(accessKeyIDs)]),
							secretKey: []byte(fakeSecret),
						},
					},
				)
			})
		}

		// Returns only once every caller is durably blocked. The cache cannot have
		// served any of them, so the only place they can be is waiting on an
		// acquisition. Specifically, singleflight has started one goroutine per
		// access key ID and each is parked inside the acquisition function, with
		// every caller waiting on the flight it joined.
		synctest.Wait()

		// Allows acquisitions to complete.
		close(release)

		// Wait for all of the callers to finish.
		wg.Wait()

		// Since we forced the callers for each access key ID to join one singleflight,
		// there should have been exactly one actual execution of the acquisition
		// function per access key ID -- no more, which would mean callers failed to
		// coalesce, and no fewer, which would mean callers for different access key IDs
		// shared an acquisition.
		require.Equal(t, int32(len(accessKeyIDs)), acquisitions.Load())

		// Verify that every caller received the credentials for its own access key
		// ID and no errors.
		for i := range concurrency {
			require.NoError(t, errs[i])
			require.NotNil(t, creds[i])
			require.Equal(t, "AWS", creds[i].Username)
			require.Equal(t, accessKeyIDs[i%len(accessKeyIDs)], creds[i].Password)
		}
	})
}

func TestAccessKeyProvider_GetCredentials_winnerCanceled(t *testing.T) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context
	// detached from any caller's, it must survive that and still serve the
	// remaining callers waiting on it.

	const (
		fakeRepoURL = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
		fakeRegion  = "us-west-2"
		fakeID      = "AKIAIOSFODNN7EXAMPLE"                     // nolint:gosec
		fakeSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // nolint:gosec
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

		provider := &AccessKeyProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAuthTokenFn: func(
				ctx context.Context,
				_ string,
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
			RepoURL: fakeRepoURL,
			Data: map[string][]byte{
				regionKey: []byte(fakeRegion),
				idKey:     []byte(fakeID),
				secretKey: []byte(fakeSecret),
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
			require.Equal(t, "AWS", res.creds.Username)
			require.Equal(t, "password", res.creds.Password)
		}
	})
}

func TestAccessKeyProvider_GetCredentials_acquisitionTimeout(t *testing.T) {
	// An acquisition runs under a context detached from every caller's, so a
	// caller giving up cannot end it. Its own deadline is the only thing that
	// can, and until it does, the singleflight key stays occupied and no fresh
	// acquisition for that key can begin.

	const (
		fakeRepoURL = "123456789012.dkr.ecr.us-west-2.amazonaws.com/my-repo"
		fakeRegion  = "us-west-2"
		fakeID      = "AKIAIOSFODNN7EXAMPLE"                     // nolint:gosec
		fakeSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // nolint:gosec
	)

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. A bubble has a fake
	// clock that advances only when every goroutine in the group is durably
	// blocked, and then only as far as the next pending timer. Here that timer is
	// the acquisition's own deadline, so this test reaches it instantly and
	// measures it exactly, instead of waiting 30 seconds for it.
	synctest.Test(t, func(t *testing.T) {
		provider := &AccessKeyProvider{
			tokenCache: expiring.NewAlwaysMissing(),
			getAuthTokenFn: func(
				ctx context.Context,
				_ string,
				_ string,
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
				RepoURL: fakeRepoURL,
				Data: map[string][]byte{
					regionKey: []byte(fakeRegion),
					idKey:     []byte(fakeID),
					secretKey: []byte(fakeSecret),
				},
			},
		)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Nil(t, creds)
		require.Equal(t, tokenAcquisitionTimeout, time.Since(start))
	})
}
