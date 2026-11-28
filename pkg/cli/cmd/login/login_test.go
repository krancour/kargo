package login

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	libConfig "github.com/akuity/kargo/pkg/cli/config"
)

func TestNewCommand_preRun(t *testing.T) {
	testCases := []struct {
		name   string
		args   []string
		flags  []string
		assert func(*testing.T, error)
	}{
		{
			name: "server specified without a login method",
			args: []string{"https://kargo.example.com"},
			assert: func(t *testing.T, err error) {
				require.ErrorContains(
					t,
					err,
					"at least one of the flags in the group [admin sso] is required",
				)
			},
		},
		{
			name:  "server specified with a login method",
			args:  []string{"https://kargo.example.com"},
			flags: []string{"--sso", "--device-code"},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "no server specified and no flags",
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:  "no server specified with --insecure-skip-tls-verify",
			flags: []string{"--insecure-skip-tls-verify"},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:  "no server specified with a login method",
			flags: []string{"--sso"},
			assert: func(t *testing.T, err error) {
				require.ErrorContains(
					t,
					err,
					"--sso can only be used with a server address",
				)
			},
		},
		{
			name:  "no server specified with another flag",
			flags: []string{"--device-code"},
			assert: func(t *testing.T, err error) {
				require.ErrorContains(
					t,
					err,
					"--device-code can only be used with a server address",
				)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewCommand(
				libConfig.CLIConfig{},
				genericiooptions.NewTestIOStreamsDiscard(),
			)
			require.NoError(t, cmd.ParseFlags(testCase.flags))
			err := cmd.PreRunE(cmd, testCase.args)
			if err == nil {
				err = cmd.ValidateFlagGroups()
			}
			testCase.assert(t, err)
		})
	}
}

func TestNewCommand_mutuallyExclusiveFlags(t *testing.T) {
	groupErr := func(group string) func(*testing.T, error) {
		return func(t *testing.T, err error) {
			require.ErrorContains(
				t,
				err,
				fmt.Sprintf("if any flags in the group [%s] are set", group),
			)
		}
	}
	noErr := func(t *testing.T, err error) {
		require.NoError(t, err)
	}
	testCases := []struct {
		name   string
		flags  []string
		assert func(*testing.T, error)
	}{
		{
			name:   "admin and SSO",
			flags:  []string{"--admin", "--sso"},
			assert: groupErr("admin sso"),
		},
		{
			name:   "password for admin user login",
			flags:  []string{"--admin", "--password", "fake-password"},
			assert: noErr,
		},
		{
			name:   "password for SSO login",
			flags:  []string{"--sso", "--password", "fake-password"},
			assert: groupErr("sso password"),
		},
		{
			name:   "port for SSO login",
			flags:  []string{"--sso", "--port", "8085"},
			assert: noErr,
		},
		{
			name:   "port for admin user login",
			flags:  []string{"--admin", "--port", "0"},
			assert: groupErr("admin port"),
		},
		{
			name:   "device code for SSO login",
			flags:  []string{"--sso", "--device-code"},
			assert: noErr,
		},
		{
			name:   "device code for admin user login",
			flags:  []string{"--admin", "--device-code"},
			assert: groupErr("admin device-code"),
		},
		{
			name:   "device code and port",
			flags:  []string{"--sso", "--device-code", "--port", "8085"},
			assert: groupErr("device-code port"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewCommand(
				libConfig.CLIConfig{},
				genericiooptions.NewTestIOStreamsDiscard(),
			)
			require.NoError(t, cmd.ParseFlags(testCase.flags))
			testCase.assert(t, cmd.ValidateFlagGroups())
		})
	}
}

func Test_loginOptions_complete(t *testing.T) {
	storedConfig := libConfig.CLIConfig{
		APIAddress:            "https://kargo.example.com",
		AuthMethod:            authMethodSSO,
		CallbackPort:          8085,
		UseDeviceCode:         true,
		InsecureSkipTLSVerify: true,
	}
	testCases := []struct {
		name   string
		config libConfig.CLIConfig
		args   []string
		assert func(*testing.T, *loginOptions)
	}{
		{
			name:   "server specified",
			config: storedConfig,
			args:   []string{"kargo.example.org"},
			assert: func(t *testing.T, o *loginOptions) {
				require.Equal(t, "https://kargo.example.org", o.ServerAddress)
				require.False(t, o.UseAdmin)
				require.False(t, o.UseSSO)
				require.Zero(t, o.CallbackPort)
				require.False(t, o.UseDeviceCode)
			},
		},
		{
			name:   "server specified that matches the stored one",
			config: storedConfig,
			args:   []string{"https://kargo.example.com"},
			assert: func(t *testing.T, o *loginOptions) {
				require.Equal(t, "https://kargo.example.com", o.ServerAddress)
				require.False(t, o.UseSSO)
				require.Zero(t, o.CallbackPort)
				require.False(t, o.UseDeviceCode)
			},
		},
		{
			name:   "no server specified",
			config: storedConfig,
			assert: func(t *testing.T, o *loginOptions) {
				require.Equal(t, "https://kargo.example.com", o.ServerAddress)
				require.False(t, o.UseAdmin)
				require.True(t, o.UseSSO)
				require.Equal(t, 8085, o.CallbackPort)
				require.True(t, o.UseDeviceCode)
				require.False(t, o.InsecureTLS)
			},
		},
		{
			name: "no server specified with stored admin user login",
			config: libConfig.CLIConfig{
				APIAddress: "https://kargo.example.com",
				AuthMethod: authMethodAdmin,
			},
			assert: func(t *testing.T, o *loginOptions) {
				require.True(t, o.UseAdmin)
				require.False(t, o.UseSSO)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			o := &loginOptions{Config: testCase.config}
			o.complete(testCase.args)
			testCase.assert(t, o)
		})
	}
}

func Test_loginOptions_validate(t *testing.T) {
	testCases := []struct {
		name   string
		config libConfig.CLIConfig
		args   []string
		useSSO bool
		assert func(*testing.T, error)
	}{
		{
			name: "no server address",
			assert: func(t *testing.T, err error) {
				require.ErrorContains(t, err, "server address is required")
			},
		},
		{
			name: "no stored login method",
			config: libConfig.CLIConfig{
				APIAddress: "https://kargo.example.com",
				AuthMethod: "kubeconfig",
			},
			assert: func(t *testing.T, err error) {
				require.ErrorContains(t, err, "no login method is stored")
			},
		},
		{
			name: "stored login",
			config: libConfig.CLIConfig{
				APIAddress:    "https://kargo.example.com",
				AuthMethod:    authMethodSSO,
				UseDeviceCode: true,
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "server address and login method specified",
			args:   []string{"kargo.example.com"},
			useSSO: true,
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			o := &loginOptions{
				Config: testCase.config,
				UseSSO: testCase.useSSO,
			}
			o.complete(testCase.args)
			testCase.assert(t, o.validate())
		})
	}
}

func TestSSOLogin_deviceCode(t *testing.T) {
	testCases := []struct {
		name          string
		deviceHandler http.HandlerFunc
		assert        func(t *testing.T, idToken, refreshToken string, err error)
	}{
		{
			name: "identity provider does not support the device authorization grant",
			assert: func(t *testing.T, _, _ string, err error) {
				require.ErrorContains(
					t,
					err,
					"identity provider does not support the device authorization grant",
				)
			},
		},
		{
			name: "success",
			deviceHandler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "fake-cli-client", r.FormValue("client_id"))
				assert.Contains(
					t,
					strings.Fields(r.FormValue("scope")),
					"offline_access",
				)
				deviceAuthHandler(false, 60, 1)(w, r)
			},
			assert: func(t *testing.T, idToken, refreshToken string, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-id-token", idToken)
				require.Equal(t, "fake-refresh-token", refreshToken)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			srv := newFakeKargoAndIdP(t, testCase.deviceHandler, tokenSuccessHandler)
			o := &loginOptions{
				IOStreams:     genericiooptions.NewTestIOStreamsDiscard(),
				ServerAddress: srv.URL,
				UseDeviceCode: true,
			}
			idToken, refreshToken, err := o.ssoLogin(t.Context())
			testCase.assert(t, idToken, refreshToken, err)
		})
	}
}

func TestAuthCodeLogin(t *testing.T) {
	testCases := []struct {
		name string
		// browserOpens indicates whether the browser opens the authorization URL.
		// If it doesn't, the user opens the URL printed by the CLI instead.
		browserOpens bool
		assert       func(t *testing.T, token *oauth2.Token, output string, err error)
	}{
		{
			name:         "browser opens",
			browserOpens: true,
			assert: func(t *testing.T, token *oauth2.Token, output string, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-id-token", token.Extra("id_token"))
				require.Contains(t, output, "open the following URL")
				require.Contains(t, output, "/auth?")
			},
		},
		{
			name: "browser fails to open",
			assert: func(t *testing.T, token *oauth2.Token, output string, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-id-token", token.Extra("id_token"))
				require.Contains(t, output, "open the following URL")
				require.Contains(t, output, "/auth?")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			mux.HandleFunc("POST /token", authCodeTokenHandler)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			oauthCfg := oauth2.Config{
				ClientID: "fake-client",
				Endpoint: oauth2.Endpoint{
					AuthURL:  srv.URL + "/auth",
					TokenURL: srv.URL + "/token",
				},
				Scopes: []string{"openid"},
			}
			user := &fakeUser{t: t, opensURLs: !testCase.browserOpens}
			o := &loginOptions{
				IOStreams: genericiooptions.IOStreams{
					Out:    io.Discard,
					ErrOut: user,
				},
			}
			openBrowser := func(authURL string) error {
				if !testCase.browserOpens {
					return errors.New("no browser")
				}
				go completeAuthCodeFlow(t, authURL)
				return nil
			}
			// Fail rather than wait for the login's own timeout if no one ever
			// calls back.
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			token, err := o.authCodeLogin(ctx, oauthCfg, openBrowser)
			testCase.assert(t, token, user.String(), err)
		})
	}
}

func TestDeviceCodeLogin(t *testing.T) {
	testCases := []struct {
		name          string
		deviceHandler http.HandlerFunc
		tokenHandler  http.HandlerFunc
		assert        func(t *testing.T, token *oauth2.Token, output string, err error)
	}{
		{
			name: "no device authorization endpoint",
			assert: func(t *testing.T, _ *oauth2.Token, output string, err error) {
				require.ErrorContains(
					t,
					err,
					"identity provider does not support the device authorization grant",
				)
				require.Empty(t, output)
			},
		},
		{
			name: "device authorization request fails",
			deviceHandler: func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(
					w,
					http.StatusBadRequest,
					map[string]string{"error": "unauthorized_client"},
				)
			},
			assert: func(t *testing.T, _ *oauth2.Token, output string, err error) {
				require.ErrorContains(t, err, "error requesting device authorization")
				require.ErrorContains(t, err, "unauthorized_client")
				require.Empty(t, output)
			},
		},
		{
			name:          "user denies access",
			deviceHandler: deviceAuthHandler(false, 60, 1),
			tokenHandler:  tokenErrorHandler("access_denied"),
			assert: func(t *testing.T, _ *oauth2.Token, _ string, err error) {
				require.ErrorContains(t, err, "error waiting for device authorization")
				require.ErrorContains(t, err, "access_denied")
			},
		},
		{
			name: "device code expires",
			// The code expires before the first poll is due.
			deviceHandler: deviceAuthHandler(false, 1, 2),
			tokenHandler:  tokenErrorHandler("authorization_pending"),
			assert: func(t *testing.T, _ *oauth2.Token, _ string, err error) {
				require.EqualError(
					t,
					err,
					"timed out waiting for user to complete authentication",
				)
			},
		},
		{
			name:          "verification URL includes the user code",
			deviceHandler: deviceAuthHandler(true, 60, 1),
			tokenHandler: pendingThenHandler(
				1,
				tokenSuccessHandler,
			),
			assert: func(t *testing.T, token *oauth2.Token, output string, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-id-token", token.Extra("id_token"))
				require.Contains(t, output, "confirm that it shows the code FAKE-CODE")
				require.Contains(
					t,
					output,
					"https://idp.example.com/device?user_code=FAKE-CODE",
				)
			},
		},
		{
			name:          "verification URL does not include the user code",
			deviceHandler: deviceAuthHandler(false, 60, 1),
			tokenHandler:  tokenSuccessHandler,
			assert: func(t *testing.T, token *oauth2.Token, output string, err error) {
				require.NoError(t, err)
				require.Equal(t, "fake-id-token", token.Extra("id_token"))
				require.Contains(t, output, "enter the code FAKE-CODE")
				require.Contains(t, output, "https://idp.example.com/device\n")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			if testCase.deviceHandler != nil {
				mux.HandleFunc("POST /device", testCase.deviceHandler)
			}
			if testCase.tokenHandler != nil {
				mux.HandleFunc("POST /token", testCase.tokenHandler)
			}
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			oauthCfg := oauth2.Config{
				ClientID: "fake-client",
				Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"},
				Scopes:   []string{"openid"},
			}
			if testCase.deviceHandler != nil {
				oauthCfg.Endpoint.DeviceAuthURL = srv.URL + "/device"
			}
			streams, _, _, errOut := genericiooptions.NewTestIOStreams()
			o := &loginOptions{IOStreams: streams}
			token, err := o.deviceCodeLogin(t.Context(), oauthCfg)
			testCase.assert(t, token, errOut.String(), err)
		})
	}
}

func TestReceiveAuthCode(t *testing.T) {
	const (
		testState = "fake-state"
		testCode  = "fake-code"
	)
	ctx := t.Context()
	codeCh := make(chan string)
	errCh := make(chan error)
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	go receiveAuthCode(ctx, listener, testState, codeCh, errCh)
	go func() {
		data := url.Values{
			"state": {testState},
			"code":  {testCode},
		}
		var res *http.Response
		res, err = http.PostForm(
			fmt.Sprintf("http://%s/auth/callback", listener.Addr().String()),
			data,
		)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
	}()
	select {
	case code := <-codeCh: // Success
		require.Equal(t, testCode, code)
	case err = <-errCh:
		require.FailNow(t, "", "received unexpected error: %s", err)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "timed out waiting for auth code")
	case <-ctx.Done():
		require.FailNow(t, "context canceled while waiting for auth code")
	}
}

func TestCreatePCKEVerifierAndChallenge(t *testing.T) {
	codeVerifier, codeChallenge, err := createPCKEVerifierAndChallenge()
	require.NoError(t, err)
	// Assert that the returned code verifier correctly answers the code challenge
	codeChallengeBytes, err := base64.RawURLEncoding.DecodeString(codeChallenge)
	require.NoError(t, err)
	expectedCodeChallengeBytes := sha256.Sum256([]byte(codeVerifier))
	require.Equal(
		t,
		expectedCodeChallengeBytes[:], // [:] converts [32]byte to []byte
		codeChallengeBytes,
	)
}

func TestRandStringFromCharset(t *testing.T) {
	const (
		minLen   = 10
		interval = 10
		maxLen   = 110
		chartSet = defaultRandStringCharSet
	)
	// Try a variety of lengths to ensure that the random string generator honors
	// the requested length
	for i := minLen; i <= maxLen; i += interval {
		set := map[string]struct{}{}
		// Generate many random strings of the same length to assert randomness
		for j := 0; j < 100; j++ {
			// Assert that the random string is the correct length
			str, err := randStringFromCharset(i, chartSet)
			require.NoError(t, err)
			require.Len(t, str, i)
			// Assert that the random string is composed only of characters from the
			// specified character set
			for _, c := range str {
				require.Contains(t, chartSet, string(c))
			}
			// Assert that the each new random string is not a duplicate of any that
			// preceded it. A duplicate would suggest inadequate randomness.
			_, ok := set[str]
			require.False(t, ok)
			set[str] = struct{}{}
		}
	}
}

func Test_normalizeURL(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{
			address: "example.com",
			want:    "https://example.com",
		},
		{
			address: "example.com:8080",
			want:    "https://example.com:8080",
		},
		{
			address: "http://example.com",
			want:    "http://example.com",
		},
		{
			address: "https://example.com",
			want:    "https://example.com",
		},
		{
			address: " ftp://example.com",
			want:    "ftp://example.com",
		},
		{
			address: "  example.com/path  ",
			want:    "https://example.com/path",
		},
		{
			address: "localhost",
			want:    "https://localhost",
		},
		{
			address: "localhost:3000",
			want:    "https://localhost:3000",
		},
		{
			address: "",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeURL(tt.address))
		})
	}
}

// newFakeKargoAndIdP returns a test server that acts as both a Kargo API
// server and the OpenID Connect identity provider it is configured to use.
// The identity provider advertises a device authorization endpoint only if
// deviceHandler is non-nil.
func newFakeKargoAndIdP(
	t *testing.T,
	deviceHandler http.HandlerFunc,
	tokenHandler http.HandlerFunc,
) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /v1beta1/system/public-server-config",
		func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"oidcConfig": map[string]any{
					"issuerUrl":   "http://" + r.Host,
					"clientId":    "fake-client",
					"cliClientId": "fake-cli-client",
					"scopes":      []string{"openid", "email"},
				},
			})
		},
	)
	mux.HandleFunc(
		"GET /.well-known/openid-configuration",
		func(w http.ResponseWriter, r *http.Request) {
			issuer := "http://" + r.Host
			discovery := map[string]any{
				"issuer":                 issuer,
				"authorization_endpoint": issuer + "/auth",
				"token_endpoint":         issuer + "/token",
				"jwks_uri":               issuer + "/keys",
				"scopes_supported":       []string{"openid", "email", "offline_access"},
			}
			if deviceHandler != nil {
				discovery["device_authorization_endpoint"] = issuer + "/device"
			}
			writeJSON(w, http.StatusOK, discovery)
		},
	)
	if deviceHandler != nil {
		mux.HandleFunc("POST /device", deviceHandler)
	}
	mux.HandleFunc("POST /token", tokenHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// deviceAuthHandler returns a handler that answers a device authorization
// request. The verification URL includes the user code only if complete is
// true.
func deviceAuthHandler(complete bool, expiresIn, interval int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res := map[string]any{
			"device_code":      "fake-device-code",
			"user_code":        "FAKE-CODE",
			"verification_uri": "https://idp.example.com/device",
			"expires_in":       expiresIn,
			"interval":         interval,
		}
		if complete {
			res["verification_uri_complete"] =
				"https://idp.example.com/device?user_code=FAKE-CODE"
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// tokenSuccessHandler answers a token request with a token response that
// includes an ID token and a refresh token.
func tokenSuccessHandler(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" ||
		r.FormValue("device_code") != "fake-device-code" {
		writeJSON(
			w,
			http.StatusBadRequest,
			map[string]string{"error": "invalid_grant"},
		)
		return
	}
	writeTokenResponse(w)
}

// fakeUser records what the CLI writes for the user. If opensURLs is true, it
// also opens any URL it is shown, as a user would when no browser opens.
type fakeUser struct {
	t         *testing.T
	opensURLs bool
	mu        sync.Mutex
	output    bytes.Buffer
}

func (f *fakeUser) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opensURLs {
		for _, field := range strings.Fields(string(p)) {
			if strings.HasPrefix(field, "http://") {
				go completeAuthCodeFlow(f.t, field)
			}
		}
	}
	return f.output.Write(p)
}

func (f *fakeUser) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.output.String()
}

// completeAuthCodeFlow stands in for a web browser and an identity provider.
// Given an authorization URL, it calls back to the URL's redirect URI with an
// authorization code, as the identity provider would after the user
// authenticates.
func completeAuthCodeFlow(t *testing.T, authURL string) {
	u, err := url.Parse(authURL)
	if !assert.NoError(t, err) {
		return
	}
	query := u.Query()
	callbackURL := query.Get("redirect_uri") + "?" + url.Values{
		"code":  []string{"fake-auth-code"},
		"state": []string{query.Get("state")},
	}.Encode()
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		callbackURL,
		nil,
	)
	if !assert.NoError(t, err) {
		return
	}
	res, err := http.DefaultClient.Do(req)
	if !assert.NoError(t, err) {
		return
	}
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

// authCodeTokenHandler answers a request to exchange the authorization code
// issued by completeAuthCodeFlow with a token response that includes an ID
// token and a refresh token.
func authCodeTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("grant_type") != "authorization_code" ||
		r.FormValue("code") != "fake-auth-code" ||
		r.FormValue("code_verifier") == "" {
		writeJSON(
			w,
			http.StatusBadRequest,
			map[string]string{"error": "invalid_grant"},
		)
		return
	}
	writeTokenResponse(w)
}

// tokenErrorHandler returns a handler that answers every token request with
// the specified OAuth 2.0 error code.
func tokenErrorHandler(code string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
	}
}

// pendingThenHandler returns a handler that answers the first n token
// requests with authorization_pending and every request after that with next.
func pendingThenHandler(n int32, next http.HandlerFunc) http.HandlerFunc {
	var requests atomic.Int32
	pending := tokenErrorHandler("authorization_pending")
	return func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= n {
			pending(w, r)
			return
		}
		next(w, r)
	}
}

// writeTokenResponse writes a token response that includes an ID token and a
// refresh token.
func writeTokenResponse(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  "fake-access-token",
		"token_type":    "Bearer",
		"expires_in":    3600,
		"id_token":      "fake-id-token",
		"refresh_token": "fake-refresh-token",
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
