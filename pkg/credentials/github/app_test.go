package github

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
)

func TestNewAppCredentialProvider(t *testing.T) {
	provider := NewAppCredentialProvider().(*AppCredentialProvider) // nolint:forcetypeassert
	assert.NotNil(t, provider)

	assert.NotNil(t, provider.tokenCache)
	assert.NotEmpty(t, provider.validationBackoff)
	assert.NotZero(t, provider.mintTimeoutBuffer)
	assert.NotNil(t, provider.getAccessTokenFn)
	assert.NotNil(t, provider.validateAccessTokenFn)
}

func TestAppCredentialProvider_Supports(t *testing.T) {
	p := NewAppCredentialProvider()

	const testRepoURL = "https://github.com/example/repo"
	// This is a control. Each test case will tweak a clone of this supported map.
	supportedDataMap := map[string][]byte{
		clientIDKey:       []byte("client"),
		appIDKey:          []byte("123"),
		installationIDKey: []byte("456"),
		privateKeyKey:     []byte("private-key"),
	}
	supports, err := p.Supports(
		t.Context(),
		credentials.Request{
			Type:    credentials.TypeGit,
			RepoURL: testRepoURL,
			Data:    supportedDataMap,
		},
	)
	require.NoError(t, err)
	require.True(t, supports)

	testCases := []struct {
		name       string
		credType   credentials.Type
		repoURL    string
		getDataMap func() map[string][]byte
		expected   bool
	}{
		{
			name:     "non-Git credential type",
			credType: credentials.Type("other"),
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				return supportedDataMap
			},
			expected: false,
		},
		{
			name:     "nil data map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				return nil
			},
			expected: false,
		},
		{
			name:     "empty data map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				return map[string][]byte{}
			},
			expected: false,
		},
		{
			name:     "not an http/s URL",
			credType: credentials.TypeGit,
			repoURL:  "git@github.com:example/repo.git",
			getDataMap: func() map[string][]byte {
				return supportedDataMap
			},
			expected: false,
		},
		{
			name:     "no client ID or app ID in data map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				delete(dm, appIDKey)
				delete(dm, clientIDKey)
				return dm
			},
			expected: false,
		},
		{
			name:     "client ID and app ID are empty",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				dm[appIDKey] = []byte("")
				dm[clientIDKey] = []byte("")
				return dm
			},
			expected: false,
		},
		{
			name:     "no installation ID in data map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				delete(dm, installationIDKey)
				return dm
			},
			expected: false,
		},
		{
			name:     "installation ID is empty",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				dm[installationIDKey] = []byte("")
				return dm
			},
			expected: false,
		},
		{
			name:     "no private key in data map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				delete(dm, privateKeyKey)
				return dm
			},
			expected: false,
		},
		{
			name:     "private key is empty",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				dm[privateKeyKey] = []byte("")
				return dm
			},
			expected: false,
		},
		{
			name:     "valid with client ID",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				delete(dm, appIDKey)
				return dm
			},
			expected: true,
		},
		{
			name:     "valid with App ID",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			getDataMap: func() map[string][]byte {
				dm := maps.Clone(supportedDataMap)
				delete(dm, clientIDKey)
				return dm
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			supports, err := p.Supports(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
					Data:    testCase.getDataMap(),
				},
			)
			require.NoError(t, err)
			require.Equal(t, testCase.expected, supports)
		})
	}
}

func TestAppCredentialProvider_GetCredentials(t *testing.T) {
	const testProject = "fake-project"
	const testRepoName = "repo"
	testRepoURL := fmt.Sprintf("https://github.com/example/%s", testRepoName)
	testData := map[string][]byte{
		appIDKey:          []byte("123"),
		installationIDKey: []byte("456"),
		privateKeyKey:     []byte("private-key"),
	}

	testCases := []struct {
		name             string
		credType         credentials.Type
		repoURL          string
		data             map[string][]byte
		metadata         map[string]string
		getAccessTokenFn func(
			appOrClientID string,
			installationID int64,
			encodedPrivateKey string,
			repoURL string,
		) (*oauth2.Token, error)
		assertions func(t *testing.T, creds *credentials.Credentials, err error)
	}{
		{
			name:     "cannot extract repo name from URL",
			credType: credentials.TypeGit,
			repoURL:  "https://github.com/example", // Looks like an org; not a repo
			data:     testData,
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "error unmarshaling scope map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			metadata: map[string]string{
				kargoapi.AnnotationKeyGitHubTokenScope: "invalid json",
			},
			assertions: func(t *testing.T, _ *credentials.Credentials, err error) {
				assert.ErrorContains(t, err, "error unmarshaling scope map")
			},
		},
		{
			name:     "project has no entry in scope map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			metadata: map[string]string{
				kargoapi.AnnotationKeyGitHubTokenScope: `{}`,
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "project has nil entry in scope map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			metadata: map[string]string{
				kargoapi.AnnotationKeyGitHubTokenScope: fmt.Sprintf(`{%q: null}`, testProject),
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "project has empty entry in scope map",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			metadata: map[string]string{
				kargoapi.AnnotationKeyGitHubTokenScope: fmt.Sprintf(`{%q: []}`, testProject),
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "invalid installation ID",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data: map[string][]byte{
				appIDKey:          []byte("123"),
				installationIDKey: []byte("invalid"),
				privateKeyKey:     []byte("private-key"),
			},
			// We'll limit the test project to accessing only the test repo. If we
			// get as far as the error parsing the installation token, we'll know
			// the check that the scope is allowed is working.
			metadata: map[string]string{
				kargoapi.AnnotationKeyGitHubTokenScope: fmt.Sprintf(
					`{%q: [%q]}`, testProject, testRepoName,
				),
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.Error(t, err)
				assert.ErrorContains(t, err, "error parsing installation ID")
			},
		},
		// From here on out, we won't include any scope map...
		{
			name:     "error getting token",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			getAccessTokenFn: func(string, int64, string, string) (*oauth2.Token, error) {
				return nil, errors.New("token error")
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.Nil(t, creds)
				assert.Error(t, err)
				assert.ErrorContains(t, err, "token error")
			},
		},
		{
			name:     "successful token retrieval",
			credType: credentials.TypeGit,
			repoURL:  testRepoURL,
			data:     testData,
			getAccessTokenFn: func(string, int64, string, string) (*oauth2.Token, error) {
				return &oauth2.Token{
					AccessToken: "test-token",
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
			assertions: func(t *testing.T, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, "test-token", creds.Password)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := NewAppCredentialProvider().(*AppCredentialProvider) // nolint:forcetypeassert
			provider.validateAccessTokenFn = func(
				context.Context, string, string,
			) (bool, error) {
				return true, nil
			}

			if testCase.getAccessTokenFn != nil {
				provider.getAccessTokenFn = testCase.getAccessTokenFn
			}

			creds, err := provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:     testCase.credType,
					Project:  testProject,
					RepoURL:  testCase.repoURL,
					Data:     testCase.data,
					Metadata: testCase.metadata,
				},
			)
			testCase.assertions(t, creds, err)
		})
	}
}

func TestAppCredentialProvider_getUsernameAndPassword(t *testing.T) {
	const (
		fakeAppOrClientID  = "fake-id"
		fakeInstallationID = int64(456)
		fakePrivateKey     = "private-key"
		fakeRepoURL        = "https://github.com/example/repo"
		fakeRepoName       = "repo"
		fakeAccessToken    = "test-token"
	)

	p := &AppCredentialProvider{}

	testTokenCacheKey := p.tokenCacheKey(
		fakeAppOrClientID,
		fakeInstallationID,
		fakePrivateKey,
		fakeRepoURL,
	)

	testCases := []struct {
		name             string
		setupCache       func(c expiring.Cache)
		getAccessTokenFn func(
			appOrClientID string,
			installationID int64,
			encodedPrivateKey string,
			repoURL string,
		) (*oauth2.Token, error)
		assertions func(*testing.T, expiring.Cache, *credentials.Credentials, error)
	}{
		{
			name: "cache hit",
			setupCache: func(c expiring.Cache) {
				c.Set(testTokenCacheKey, fakeAccessToken, cache.DefaultExpiration)
			},
			assertions: func(
				t *testing.T,
				_ expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeAccessToken, creds.Password)
			},
		},
		{
			name: "cache miss, successful token fetch",
			getAccessTokenFn: func(string, int64, string, string) (*oauth2.Token, error) {
				return &oauth2.Token{
					AccessToken: fakeAccessToken,
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
			assertions: func(
				t *testing.T,
				c expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, accessTokenUsername, creds.Username)
				assert.Equal(t, fakeAccessToken, creds.Password)

				// Verify the token was cached with a TTL based on the
				// token's actual expiry
				realCache, ok := c.(*cache.Cache)
				require.True(t, ok)
				items := realCache.Items()
				item, found := items[testTokenCacheKey]
				assert.True(t, found)
				expectedTTL := 55 * time.Minute // 1h expiry - 5m margin
				actualTTL := time.Until(time.Unix(0, item.Expiration))
				assert.InDelta(t, expectedTTL.Seconds(), actualTTL.Seconds(), 5)
			},
		},
		{
			name: "error in getAccessToken",
			getAccessTokenFn: func(string, int64, string, string) (*oauth2.Token, error) {
				return nil, errors.New("token error")
			},
			assertions: func(
				t *testing.T,
				c expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.ErrorContains(t, err, "token error")
				assert.Nil(t, creds)

				// Verify the token was not cached
				_, found := c.Get(testTokenCacheKey)
				assert.False(t, found)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := NewAppCredentialProvider().(*AppCredentialProvider) // nolint:forcetypeassert
			provider.validateAccessTokenFn = func(
				context.Context, string, string,
			) (bool, error) {
				return true, nil
			}

			if testCase.setupCache != nil {
				testCase.setupCache(provider.tokenCache)
			}

			if testCase.getAccessTokenFn != nil {
				provider.getAccessTokenFn = testCase.getAccessTokenFn
			}

			creds, err := provider.getUsernameAndPassword(
				t.Context(),
				fakeAppOrClientID,
				fakeInstallationID,
				fakePrivateKey,
				fakeRepoURL,
			)
			testCase.assertions(t, provider.tokenCache, creds, err)
		})
	}
}

func TestAppCredentialProvider_getUsernameAndPassword_coalescing(
	t *testing.T,
) {
	const (
		fakeAppOrClientID  = "fake-id"
		fakeInstallationID = int64(456)
		fakePrivateKey     = "private-key"

		concurrency = 10
	)

	// Callers are split evenly between two repositories. Since the cache key
	// includes the repository URL -- an access token is scoped to a single
	// repository -- the two groups must not share an acquisition, which is what
	// makes this test sensitive to an acquisition keyed on anything coarser than
	// the cache key.
	repoURLs := []string{
		"https://github.com/example/repo-a",
		"https://github.com/example/repo-b",
	}

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when every caller
	// is waiting on the acquisition, rather than guess at it with a sleep.
	synctest.Test(t, func(t *testing.T) {
		// Track the total number of actual invocations of the acquisition function.
		// If the coalescing is working, there should only ever be one per repository
		// regardless of how many concurrent callers there are.
		var acquisitions atomic.Int32

		// This channel will be used to unblock the goroutines that are parked
		// inside the acquisition function, but only after every caller is durably
		// blocked on a singleflight.
		release := make(chan struct{})

		provider := &AppCredentialProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			// The acquisition's own deadline is this plus the sum of
			// validationBackoff, so it must be non-zero or the acquisition's context
			// expires before it begins.
			mintTimeoutBuffer: time.Minute,
			getAccessTokenFn: func(
				_ string, _ int64, _ string, repoURL string,
			) (*oauth2.Token, error) {
				acquisitions.Add(1)
				<-release
				// The token identifies the repository it was obtained for, so a
				// caller served by another repository's acquisition is detectable.
				return &oauth2.Token{
					AccessToken: "token-for-" + repoURL,
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
			validateAccessTokenFn: func(
				context.Context, string, string,
			) (bool, error) {
				return true, nil
			},
		}

		creds := make([]*credentials.Credentials, concurrency)
		errs := make([]error, concurrency)
		var wg sync.WaitGroup
		for i := range concurrency {
			wg.Go(func() {
				creds[i], errs[i] = provider.getUsernameAndPassword(
					t.Context(),
					fakeAppOrClientID,
					fakeInstallationID,
					fakePrivateKey,
					repoURLs[i%len(repoURLs)],
				)
			})
		}

		// Returns only once every caller is durably blocked. The cache cannot have
		// served any of them, so the only place they can be is waiting on an
		// acquisition. Specifically, singleflight has started one goroutine per
		// repository and each is parked inside the acquisition function, with every
		// caller waiting on the flight it joined.
		synctest.Wait()

		// Allows acquisitions to complete.
		close(release)

		// Wait for all of the callers to finish.
		wg.Wait()

		// Since we forced the callers for each repository to join one singleflight,
		// there should have been exactly one actual execution of the acquisition
		// function per repository -- no more, which would mean callers failed to
		// coalesce, and no fewer, which would mean callers for different
		// repositories shared an acquisition.
		require.Equal(t, int32(len(repoURLs)), acquisitions.Load())

		// Verify that every caller received the credentials for its own repository
		// and no errors.
		for i := range concurrency {
			require.NoError(t, errs[i])
			require.NotNil(t, creds[i])
			require.Equal(t, accessTokenUsername, creds[i].Username)
			require.Equal(
				t, "token-for-"+repoURLs[i%len(repoURLs)], creds[i].Password,
			)
		}
	})
}

func TestAppCredentialProvider_getUsernameAndPassword_winnerCanceled(
	t *testing.T,
) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context detached from
	// any caller's, it must survive that and still serve the remaining callers
	// waiting on it.

	const (
		fakeAppOrClientID  = "fake-id"
		fakeInstallationID = int64(456)
		fakePrivateKey     = "private-key"
		fakeRepoURL        = "https://github.com/example/repo"
		fakeAccessToken    = "test-token"

		numWaiters = 3
	)

	// synctest.Test() runs a function in a "bubble": that function and every
	// goroutine it starts form a group the runtime tracks. Inside the bubble,
	// synctest.Wait() calls block until every other goroutine in the group is
	// durably blocked -- parked on something only another member of the group can
	// release. That is what lets this test observe the moment when the first
	// caller begins blocking on the acquisition so that waiting callers can be launched.
	// This phased launch sequence allows us to know with certainty which goroutine
	// is the winning caller and which are the waiters. Likewise, synctest.Wait()
	// allows the test to observe the moment when all of the waiting callers are
	// blocking so that we can proceed with canceling the winner's context.
	synctest.Test(t, func(t *testing.T) {
		// This channel will be used to unblock the goroutine that is parked inside
		// the acquisition function, but only after the winner has abandoned it.
		release := make(chan struct{})

		provider := &AppCredentialProvider{
			// A cache that retains nothing leaves the acquisition as the only way any
			// caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			// The acquisition's own deadline is this plus the sum of
			// validationBackoff, so it must be non-zero or the acquisition's context
			// expires before it begins.
			mintTimeoutBuffer: time.Minute,
			getAccessTokenFn: func(
				string, int64, string, string,
			) (*oauth2.Token, error) {
				return &oauth2.Token{
					AccessToken: fakeAccessToken,
					Expiry:      time.Now().Add(time.Hour),
				}, nil
			},
			// A newly acquired token is validated before being released to callers,
			// and unlike the token request itself, that step receives a context.
			// Holding it open is therefore how this test parks a goroutine inside
			// the acquisition. Honoring the context is what gives this test its teeth:
			// were the acquisition running under the winner's context instead of a
			// detached one, canceling the winner would land in the first case below
			// and every waiter would receive that error.
			validateAccessTokenFn: func(
				ctx context.Context, _, _ string,
			) (bool, error) {
				select {
				case <-ctx.Done():
					return false, ctx.Err()
				case <-release:
					return true, nil
				}
			},
		}

		// The winner is launched alone, and with a context only it holds.
		winnerCtx, cancel := context.WithCancel(t.Context())
		winnerErr := make(chan error, 1)
		go func() {
			_, err := provider.getUsernameAndPassword(
				winnerCtx,
				fakeAppOrClientID,
				fakeInstallationID,
				fakePrivateKey,
				fakeRepoURL,
			)
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
				creds, err := provider.getUsernameAndPassword(
					t.Context(),
					fakeAppOrClientID,
					fakeInstallationID,
					fakePrivateKey,
					fakeRepoURL,
				)
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

func TestAppCredentialProvider_getUsernameAndPassword_timeout(t *testing.T) {
	const (
		fakeAppOrClientID  = "fake-id"
		fakeInstallationID = int64(456)
		fakePrivateKey     = "private-key"
		fakeRepoURL        = "https://github.com/example/repo"
		fakeAccessToken    = "test-token"
	)

	provider := NewAppCredentialProvider().(*AppCredentialProvider) // nolint:forcetypeassert
	// Keep the orphaned context's deadline short so the test does not linger.
	provider.validationBackoff = nil
	provider.mintTimeoutBuffer = 50 * time.Millisecond
	provider.getAccessTokenFn = func(
		string, int64, string, string,
	) (*oauth2.Token, error) {
		return &oauth2.Token{
			AccessToken: fakeAccessToken,
			Expiry:      time.Now().Add(time.Hour),
		}, nil
	}
	// Validation never resolves on its own; it only returns once the mint's
	// orphaned context hits its deadline.
	provider.validateAccessTokenFn = func(
		ctx context.Context, _, _ string,
	) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}

	creds, err := provider.getUsernameAndPassword(
		t.Context(),
		fakeAppOrClientID,
		fakeInstallationID,
		fakePrivateKey,
		fakeRepoURL,
	)
	require.ErrorContains(t, err, "error minting installation access token")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, creds)
}

// A real (non-context) error from the mint is delivered to the caller rather
// than swallowed, and it triggers exactly one mint attempt.
func TestAppCredentialProvider_getUsernameAndPassword_realError(t *testing.T) {
	const (
		fakeAppOrClientID  = "fake-id"
		fakeInstallationID = int64(456)
		fakePrivateKey     = "private-key"
		fakeRepoURL        = "https://github.com/example/repo"
	)

	var mints atomic.Int32

	provider := NewAppCredentialProvider().(*AppCredentialProvider) // nolint:forcetypeassert
	provider.getAccessTokenFn = func(
		string, int64, string, string,
	) (*oauth2.Token, error) {
		mints.Add(1)
		return nil, errors.New("token error")
	}
	provider.validateAccessTokenFn = func(
		context.Context, string, string,
	) (bool, error) {
		return true, nil
	}

	creds, err := provider.getUsernameAndPassword(
		t.Context(),
		fakeAppOrClientID,
		fakeInstallationID,
		fakePrivateKey,
		fakeRepoURL,
	)
	require.ErrorContains(t, err, "token error")
	require.Nil(t, creds)
	// The error must not have triggered a retry.
	require.Equal(t, int32(1), mints.Load())
}

func TestAppCredentialProvider_waitForTokenUsable(t *testing.T) {
	const fakeRepoURL = "https://github.com/example/repo"

	// Keep waits between validation attempts negligible in all test cases.
	testBackoff := []time.Duration{
		time.Millisecond,
		time.Millisecond,
		time.Millisecond,
	}

	testCases := []struct {
		name                  string
		getCtx                func() context.Context
		validateAccessTokenFn func(
			attempt int,
		) (bool, error)
		assertions func(t *testing.T, attempts int, err error)
	}{
		{
			name: "token is immediately valid",
			validateAccessTokenFn: func(int) (bool, error) {
				return true, nil
			},
			assertions: func(t *testing.T, attempts int, err error) {
				require.NoError(t, err)
				require.Equal(t, 1, attempts)
			},
		},
		{
			name: "token becomes valid after retries",
			validateAccessTokenFn: func(attempt int) (bool, error) {
				return attempt >= 3, nil
			},
			assertions: func(t *testing.T, attempts int, err error) {
				require.NoError(t, err)
				require.Equal(t, 3, attempts)
			},
		},
		{
			name: "validation errors are tolerated",
			validateAccessTokenFn: func(attempt int) (bool, error) {
				if attempt == 1 {
					return false, errors.New("something went wrong")
				}
				return true, nil
			},
			assertions: func(t *testing.T, attempts int, err error) {
				require.NoError(t, err)
				require.Equal(t, 2, attempts)
			},
		},
		{
			name: "token never validates; presumed usable",
			validateAccessTokenFn: func(int) (bool, error) {
				return false, nil
			},
			assertions: func(t *testing.T, attempts int, err error) {
				require.NoError(t, err)
				// One attempt up front + one per element of the backoff
				// schedule
				require.Equal(t, len(testBackoff)+1, attempts)
			},
		},
		{
			name: "context canceled",
			getCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			validateAccessTokenFn: func(int) (bool, error) {
				return false, nil
			},
			assertions: func(t *testing.T, _ int, err error) {
				require.ErrorIs(t, err, context.Canceled)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var attempts int
			provider := &AppCredentialProvider{
				validationBackoff: testBackoff,
				validateAccessTokenFn: func(
					context.Context, string, string,
				) (bool, error) {
					attempts++
					return testCase.validateAccessTokenFn(attempts)
				},
			}
			ctx := t.Context()
			if testCase.getCtx != nil {
				ctx = testCase.getCtx()
			}
			err := provider.waitForTokenUsable(ctx, "fake-token", fakeRepoURL)
			testCase.assertions(t, attempts, err)
		})
	}
}

func TestAppCredentialProvider_validateAccessToken(t *testing.T) {
	const fakeAccessToken = "fake-token"

	testCases := []struct {
		name       string
		statusCode int
		// getRepoURL optionally overrides the repo URL used in the test case.
		// It receives the test server's URL.
		getRepoURL func(srvURL string) string
		assertions func(t *testing.T, valid bool, err error)
	}{
		{
			name:       "token is usable",
			statusCode: http.StatusOK,
			assertions: func(t *testing.T, valid bool, err error) {
				require.NoError(t, err)
				require.True(t, valid)
			},
		},
		{
			name:       "authorization failure; token is not (yet) usable",
			statusCode: http.StatusNotFound,
			assertions: func(t *testing.T, valid bool, err error) {
				require.NoError(t, err)
				require.False(t, valid)
			},
		},
		{
			name:       "unexpected status is an error",
			statusCode: http.StatusInternalServerError,
			assertions: func(t *testing.T, valid bool, err error) {
				require.ErrorContains(
					t, err, "error validating installation access token",
				)
				require.False(t, valid)
			},
		},
		{
			name: "owner and repo name cannot be extracted",
			getRepoURL: func(string) string {
				return "https://github.com/example"
			},
			assertions: func(t *testing.T, valid bool, err error) {
				require.ErrorContains(
					t, err, "could not extract repository owner and name",
				)
				require.False(t, valid)
			},
		},
	}
	p := &AppCredentialProvider{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					require.Equal(
						t, "/api/v3/repos/example/repo", r.URL.Path,
					)
					require.Equal(
						t,
						fmt.Sprintf("Bearer %s", fakeAccessToken),
						r.Header.Get("Authorization"),
					)
					w.WriteHeader(testCase.statusCode)
				},
			))
			t.Cleanup(srv.Close)
			// By default, the test server plays the role of a GitHub
			// Enterprise host
			repoURL := fmt.Sprintf("%s/example/repo", srv.URL)
			if testCase.getRepoURL != nil {
				repoURL = testCase.getRepoURL(srv.URL)
			}
			valid, err := p.validateAccessToken(
				t.Context(),
				fakeAccessToken,
				repoURL,
			)
			testCase.assertions(t, valid, err)
		})
	}
}

func TestAppCredentialProvider_decodeKey(t *testing.T) {
	const testKey = "-----BEGIN PRIVATE KEY-----\nfakekey\n-----END PRIVATE KEY-----"
	testCases := []struct {
		name       string
		key        string
		assertions func(t *testing.T, key []byte, err error)
	}{
		{
			name: "key is not base64 encoded",
			key:  testKey,
			assertions: func(t *testing.T, key []byte, err error) {
				require.NoError(t, err)
				require.Equal(t, []byte(testKey), key)
			},
		},
		{
			name: "key is base64 encoded",
			key:  base64.StdEncoding.EncodeToString([]byte(testKey)),
			assertions: func(t *testing.T, key []byte, err error) {
				require.NoError(t, err)
				require.Equal(t, []byte(testKey), key)
			},
		},
		{
			name: "key is a corrupted base64 encoding",
			key:  "corrupted", // These are all base64 digits. :)
			assertions: func(t *testing.T, _ []byte, err error) {
				require.ErrorContains(t, err, "probable corrupt base64 encoding")
			},
		},
	}
	p := &AppCredentialProvider{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			key, err := p.decodeKey(testCase.key)
			testCase.assertions(t, key, err)
		})
	}
}

func TestAppCredentialProvider_extractRepoName(t *testing.T) {
	testCases := []struct {
		name     string
		repoURL  string
		expected string
	}{
		{
			name:     "invalid repo URL",
			repoURL:  "https://github.com/akuity",
			expected: "",
		},
		{
			name:     "GitHub URL",
			repoURL:  "https://github.com/example/repo",
			expected: "repo",
		},
		{
			name:     "GitHub Enterprise URL",
			repoURL:  "https://github.example.com/example/repo",
			expected: "repo",
		},
		{
			name:     "GitHub Enterprise URL with extra path components", // Possible?
			repoURL:  "https://example.com/github/example/repo",
			expected: "repo",
		},
	}
	p := &AppCredentialProvider{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, p.extractRepoName(testCase.repoURL))
		})
	}
}

func TestAppCredentialProvider_extractBaseURL(t *testing.T) {
	testCases := []struct {
		name       string
		repoURL    string
		assertions func(t *testing.T, baseURL string, err error)
	}{
		{
			name:    "invalid URL",
			repoURL: "://invalid",
			assertions: func(t *testing.T, _ string, err error) {
				require.ErrorContains(t, err, "error parsing URL")
			},
		},
		{
			name:    "valid HTTPS URL",
			repoURL: "https://github.com/example/repo",
			assertions: func(t *testing.T, baseURL string, err error) {
				require.NoError(t, err)
				require.Equal(t, "https://github.com", baseURL)
			},
		},
		{
			name:    "valid HTTP URL",
			repoURL: "http://github.example.com/example/repo",
			assertions: func(t *testing.T, baseURL string, err error) {
				require.NoError(t, err)
				require.Equal(t, "http://github.example.com", baseURL)
			},
		},
		{
			name:    "URL with port number",
			repoURL: "https://github.example.com:8443/example/repo",
			assertions: func(t *testing.T, baseURL string, err error) {
				require.NoError(t, err)
				require.Equal(t, "https://github.example.com:8443", baseURL)
			},
		},
	}
	p := &AppCredentialProvider{}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			baseURL, err := p.extractBaseURL(testCase.repoURL)
			testCase.assertions(t, baseURL, err)
		})
	}
}
