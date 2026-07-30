package gar

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

	var acquisitions atomic.Int32

	provider := &ServiceAccountKeyProvider{
		tokenCache: cache.New(40*time.Minute, time.Hour),
		getAccessTokenFn: func(context.Context, string) (*oauth2.Token, error) {
			acquisitions.Add(1)
			// Hold the acquisition open long enough for the other goroutines to
			// pile up behind it.
			time.Sleep(50 * time.Millisecond)
			return &oauth2.Token{
				AccessToken: fakeAccessToken,
				Expiry:      time.Now().Add(time.Hour),
			}, nil
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
						serviceAccountKeyKey: []byte(fakeServiceAccountKey),
					},
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

// This exercises the case where the context of the call that wins the
// singleflight race is canceled mid-acquisition. Because the acquisition runs
// under a detached context, the winner's cancellation must NOT cancel the shared
// acquisition. The winner stops waiting and returns an "interrupted" error, but
// the single acquisition runs to completion and the coalesced callers whose
// contexts are still live receive the token from that SAME acquisition.
func TestServiceAccountKeyProvider_GetCredentials_winnerCanceled(t *testing.T) {
	const (
		fakeRepoURL           = "us-central1-docker.pkg.dev/my-project/my-repo"
		fakeServiceAccountKey = "fake-service-account-key"
		fakeAccessToken       = "fake-access-token"
	)

	var acquisitions atomic.Int32
	acquisitionStarted := make(chan struct{})
	release := make(chan struct{})

	provider := &ServiceAccountKeyProvider{
		tokenCache: cache.New(40*time.Minute, time.Hour),
		getAccessTokenFn: func(
			ctx context.Context,
			_ string,
		) (*oauth2.Token, error) {
			if acquisitions.Add(1) == 1 {
				close(acquisitionStarted)
			}
			// Hold the acquisition open until the test releases it. Were the
			// acquisition's context derived from the winner's, this would instead
			// return early with the winner's cancellation.
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

	cached, found := provider.tokenCache.Get(tokenCacheKey(fakeServiceAccountKey))
	require.True(t, found)
	require.Equal(t, fakeAccessToken, cached)
}
