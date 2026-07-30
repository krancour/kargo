package acr

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/akuity/kargo/pkg/cache/expiring"
	"github.com/akuity/kargo/pkg/credentials"
)

func TestNewWorkloadIdentityProvider(t *testing.T) {
	const azFederatedTokenFile = "AZURE_FEDERATED_TOKEN_FILE"
	const azClientID = "AZURE_CLIENT_ID"
	const azTenantID = "AZURE_TENANT_ID"
	t.Run("workload identity not available", func(t *testing.T) {
		// Make it look unavailable by ensuring key env vars are unset
		t.Setenv(azFederatedTokenFile, "") // Ensures cleanup
		os.Unsetenv(azFederatedTokenFile)  // Actually unsets
		t.Setenv(azClientID, "")           // Ensures cleanup
		os.Unsetenv(azClientID)            // Actually unsets
		t.Setenv(azTenantID, "")           // Ensures cleanup
		os.Unsetenv(azTenantID)            // Actually unsets
		require.Nil(t, NewWorkloadIdentityProvider(t.Context()))
	})
	t.Run("workload identity available", func(t *testing.T) {
		// Make it look available by ensuring key env vars are set, albeit with
		// nonsense values.
		const nonsense = "nonsense"
		t.Setenv(azFederatedTokenFile, nonsense)
		t.Setenv(azClientID, nonsense)
		t.Setenv(azTenantID, nonsense)
		require.NotNil(t, NewWorkloadIdentityProvider(t.Context()))
	})
}

func TestWorkloadIdentityProvider_Supports(t *testing.T) {
	const testOCIRepoURL = "myregistry.azurecr.io/my-repo"
	const testHTTPSRepoURL = "https://myregistry.azurecr.io/my-repo"

	testCases := []struct {
		name     string
		credType credentials.Type
		repoURL  string
		expected bool
	}{
		{
			name:     "image credential type supported",
			credType: credentials.TypeImage,
			repoURL:  testOCIRepoURL,
			expected: true,
		},
		{
			name:     "helm credential type supported",
			credType: credentials.TypeHelm,
			repoURL:  testOCIRepoURL,
			expected: true,
		},
		{
			name:     "helm HTTP/S repo URLs not supported",
			credType: credentials.TypeHelm,
			repoURL:  testHTTPSRepoURL,
			expected: false,
		},
		{
			name:     "git credential type not supported",
			credType: credentials.TypeGit,
			repoURL:  testOCIRepoURL,
			expected: false,
		},
		{
			name: "non-ACR repo URL not supported",

			credType: credentials.TypeImage,
			repoURL:  "docker.io/library/nginx",
			expected: false,
		},
	}

	p := &WorkloadIdentityProvider{credential: &mockCredential{}}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			supports, err := p.Supports(
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

func TestWorkloadIdentityProvider_GetCredentials(t *testing.T) {
	const testRepoURL = "myregistry.azurecr.io/repo"
	const testRegistryName = "myregistry"
	const testToken = "fake-access-token"

	testCases := []struct {
		name       string
		provider   *WorkloadIdentityProvider
		credType   credentials.Type
		repoURL    string
		setupCache func(cache expiring.Cache)
		assertions func(*testing.T, expiring.Cache, *credentials.Credentials, error)
	}{
		{
			name:     "not supported",
			provider: &WorkloadIdentityProvider{},
			credType: credentials.TypeGit,
			repoURL:  "git://repo",
			assertions: func(
				t *testing.T,
				_ expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "non-ACR URL",
			provider: &WorkloadIdentityProvider{},
			credType: credentials.TypeImage,
			repoURL:  "not-an-acr-url",
			assertions: func(
				t *testing.T,
				_ expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
		{
			name:     "cache hit",
			provider: &WorkloadIdentityProvider{},
			credType: credentials.TypeImage,
			repoURL:  testRepoURL,
			setupCache: func(c expiring.Cache) {
				c.Set(testRegistryName, testToken, cache.DefaultExpiration)
			},
			assertions: func(
				t *testing.T,
				_ expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, acrTokenUsername, creds.Username)
				assert.Equal(t, testToken, creds.Password)
			},
		},
		{
			name: "cache miss, successful token fetch",
			provider: &WorkloadIdentityProvider{
				getAccessTokenFn: func(_ context.Context, _ string) (string, error) {
					return testToken, nil
				},
			},
			credType: credentials.TypeImage,
			repoURL:  testRepoURL,
			assertions: func(
				t *testing.T,
				c expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.NoError(t, err)
				assert.NotNil(t, creds)
				assert.Equal(t, acrTokenUsername, creds.Username)
				assert.Equal(t, testToken, creds.Password)

				// Verify the token was cached
				cachedToken, found := c.Get(testRegistryName)
				assert.True(t, found)
				assert.Equal(t, testToken, cachedToken)
			},
		},
		{
			name: "error in getAccessToken",
			provider: &WorkloadIdentityProvider{
				getAccessTokenFn: func(_ context.Context, _ string) (string, error) {
					return "", errors.New("access token error")
				},
			},
			credType: credentials.TypeImage,
			repoURL:  testRepoURL,
			assertions: func(
				t *testing.T,
				_ expiring.Cache,
				creds *credentials.Credentials,
				err error,
			) {
				assert.ErrorContains(t, err, "error getting ACR access token")
				assert.Nil(t, creds)
			},
		},
		{
			name: "empty token from getAccessToken",
			provider: &WorkloadIdentityProvider{
				getAccessTokenFn: func(_ context.Context, _ string) (string, error) {
					return "", nil
				},
			},
			credType: credentials.TypeImage,
			repoURL:  testRepoURL,
			assertions: func(t *testing.T, _ expiring.Cache, creds *credentials.Credentials, err error) {
				assert.NoError(t, err)
				assert.Nil(t, creds)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.provider.credential = &mockCredential{}
			testCase.provider.tokenCache = cache.New(10*time.Hour, time.Hour)
			if testCase.setupCache != nil {
				testCase.setupCache(testCase.provider.tokenCache)
			}
			creds, err := testCase.provider.GetCredentials(
				t.Context(),
				credentials.Request{
					Type:    testCase.credType,
					RepoURL: testCase.repoURL,
				},
			)
			testCase.assertions(t, testCase.provider.tokenCache, creds, err)
		})
	}
}

func TestACRURLRegex(t *testing.T) {
	testCases := []struct {
		name     string
		url      string
		expected bool
		registry string
	}{
		{
			name:     "ACR URL",
			url:      "myregistry.azurecr.io/repo",
			expected: true,
			registry: "myregistry",
		},
		{
			name:     "Docker Hub URL",
			url:      "docker.io/library/nginx",
			expected: false,
		},
		{
			name:     "ECR URL",
			url:      "123456789012.dkr.ecr.us-west-2.amazonaws.com/repo",
			expected: false,
		},
		{
			name:     "Google Artifact Registry URL",
			url:      "us-central1-docker.pkg.dev/project/repo",
			expected: false,
		},
		{
			name:     "ACR URL with complex registry name",
			url:      "my-registry-123.azurecr.io/namespace/repo",
			expected: true,
			registry: "my-registry-123",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			matches := acrURLRegex.FindStringSubmatch(testCase.url)
			if testCase.expected {
				assert.Len(t, matches, 2)
				assert.Equal(t, testCase.registry, matches[1])
			} else {
				assert.Nil(t, matches, "Expected regex not to match")
			}
		})
	}
}

// mockCredential is a mock implementation of azcore.TokenCredential for testing
type mockCredential struct{}

func (m *mockCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	// Return a mock token for testing
	return azcore.AccessToken{
		Token:     "mock-access-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

func TestWorkloadIdentityProvider_GetCredentials_coalescing(t *testing.T) {
	const (
		testRepoURL = "myregistry.azurecr.io/repo"
		testToken   = "fake-access-token"

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

		provider := &WorkloadIdentityProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(context.Context, string) (string, error) {
				acquisitions.Add(1)
				<-release
				return testToken, nil
			},
		}

		req := credentials.Request{
			Type:    credentials.TypeImage,
			RepoURL: testRepoURL,
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
			require.Equal(t, acrTokenUsername, creds[i].Username)
			require.Equal(t, testToken, creds[i].Password)
		}
	})
}

func TestWorkloadIdentityProvider_GetCredentials_winnerCanceled(t *testing.T) {
	// We will test the scenario where the caller that wins the singleflight race
	// abandons it mid-acquisition. Because the acquisition runs under a context
	// detached from any caller's, it must survive that and still serve the
	// remaining callers waiting on it.

	const (
		testRepoURL = "myregistry.azurecr.io/repo"
		testToken   = "fake-access-token"

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

		provider := &WorkloadIdentityProvider{
			// A cache that retains nothing leaves the acquisition as the only way
			// any caller can obtain a token.
			tokenCache: expiring.NewAlwaysMissing(),
			getAccessTokenFn: func(ctx context.Context, _ string) (string, error) {
				// Honoring the context is what gives this test its teeth. Were the
				// acquisition running under the winner's context instead of a
				// detached one, canceling the winner would land in the first case
				// below and every waiter would receive that error.
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-release:
					return testToken, nil
				}
			},
		}

		req := credentials.Request{
			Type:    credentials.TypeImage,
			RepoURL: testRepoURL,
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
			require.Equal(t, acrTokenUsername, res.creds.Username)
			require.Equal(t, testToken, res.creds.Password)
		}
	})
}
