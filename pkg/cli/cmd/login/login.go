package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/hashicorp/go-cleanhttp"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/oauth2"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/akuity/kargo/pkg/cli/client"
	libConfig "github.com/akuity/kargo/pkg/cli/config"
	"github.com/akuity/kargo/pkg/cli/io"
	"github.com/akuity/kargo/pkg/cli/option"
	"github.com/akuity/kargo/pkg/cli/templates"
)

const (
	defaultRandStringCharSet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	authMethodAdmin = "admin"
	authMethodSSO   = "sso"
)

//go:embed assets
var assets embed.FS

type loginOptions struct {
	genericiooptions.IOStreams

	ServerAddress string

	UseAdmin bool
	// #nosec G117 -- This struct binds to CLI flags and is not transmitted
	// anywhere directly.
	Password string

	UseSSO        bool
	CallbackPort  int
	UseDeviceCode bool

	InsecureTLS bool

	Config libConfig.CLIConfig
}

func NewCommand(
	cfg libConfig.CLIConfig,
	streams genericiooptions.IOStreams,
) *cobra.Command {
	cmdOpts := &loginOptions{
		Config:    cfg,
		IOStreams: streams,
	}

	cmd := &cobra.Command{
		Use:   "login [SERVER_ADDRESS (--admin | --sso)]",
		Args:  option.MaximumNArgs(1),
		Short: "Log in to a Kargo API server",
		Example: templates.Example(`
# Log in using the admin user
kargo login https://kargo.example.com --admin

# Log in using SSO
kargo login https://kargo.example.com --sso

# Log in using SSO, completing authentication on another device
kargo login https://kargo.example.com --sso --device-code

# Log in using SSO and ignore cert warnings
kargo login https://kargo.example.com --sso --insecure-skip-tls-verify

# Log in again exactly as last time
kargo login
`),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// With a server address, the user chooses how to log in. Without one,
			// the last login is repeated exactly, so the only flag allowed is
			// --insecure-skip-tls-verify, which is never stored.
			if len(args) == 1 {
				cmd.MarkFlagsOneRequired(option.AdminFlag, option.SSOFlag)
				return nil
			}
			var err error
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if err == nil && f.Name != option.InsecureTLSFlag {
					err = fmt.Errorf(
						"--%s can only be used with a server address",
						f.Name,
					)
				}
			})
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmdOpts.complete(args)

			if err := cmdOpts.validate(); err != nil {
				return err
			}

			return cmdOpts.run(cmd.Context())
		},
	}

	// Register the option flags on the command.
	cmdOpts.addFlags(cmd)

	// Set the input/output streams for the command.
	io.SetIOStreams(cmd, cmdOpts.IOStreams)

	return cmd
}

// addFlags adds the flags for the login options to the provided command.
func (o *loginOptions) addFlags(cmd *cobra.Command) {
	option.InsecureTLS(cmd.PersistentFlags(), &o.InsecureTLS)

	option.Admin(cmd.Flags(), &o.UseAdmin,
		"Log in as the Kargo admin user. If set, --sso must not be set.")
	option.Password(cmd.Flags(), &o.Password,
		"Specify the password for non-interactive admin user login. Requires --admin.")

	option.SSO(cmd.Flags(), &o.UseSSO,
		"Log in using OpenID Connect and the server's configured identity provider. "+
			"If set, --admin must not be set.")
	option.Port(cmd.Flags(), &o.CallbackPort,
		"Port to use for the callback URL; 0 selects any available, unprivileged port. "+
			"Requires --sso.")
	option.DeviceCode(cmd.Flags(), &o.UseDeviceCode,
		"Complete authentication in a web browser on any device, using the OAuth "+
			"2.0 device authorization grant instead of a callback to localhost. "+
			"Requires --sso. If set, --port must not be set.")

	// Only one authentication method can be used at a time.
	cmd.MarkFlagsMutuallyExclusive(option.AdminFlag, option.SSOFlag)

	// Admin login cannot be used with the port or device code flags.
	cmd.MarkFlagsMutuallyExclusive(option.AdminFlag, option.PortFlag)
	cmd.MarkFlagsMutuallyExclusive(option.AdminFlag, option.DeviceCodeFlag)

	// SSO login cannot be used with the password flag.
	cmd.MarkFlagsMutuallyExclusive(option.SSOFlag, option.PasswordFlag)
	// And the device code flag cannot be used with the port flag.
	cmd.MarkFlagsMutuallyExclusive(option.DeviceCodeFlag, option.PortFlag)
}

// complete sets the options from the command arguments. If no server address
// is specified, it sets them from the stored configuration instead, so that the
// last login is repeated exactly.
func (o *loginOptions) complete(args []string) {
	// If the server address was provided as a positional argument, use it.
	if len(args) == 1 {
		o.ServerAddress = normalizeURL(args[0])
		return
	}

	// If we get to here, no server address was provided as a positional argument,
	// so we fall back to the stored configuration.
	o.ServerAddress = o.Config.APIAddress
	o.UseAdmin = o.Config.AuthMethod == authMethodAdmin
	o.UseSSO = o.Config.AuthMethod == authMethodSSO
	o.CallbackPort = o.Config.CallbackPort
	o.UseDeviceCode = o.Config.UseDeviceCode
	// Note: We deliberately do NOT inherit o.InsecureTLS from the stored config
	// because we want to force the user to re-assess that choice each time they
	// log in.
}

// validate performs validation of the options after complete has set them. If
// the options are invalid, an error is returned.
func (o *loginOptions) validate() error {
	if o.ServerAddress == "" {
		return errors.New("server address is required")
	}
	if !o.UseAdmin && !o.UseSSO {
		return fmt.Errorf(
			"no login method is stored; specify a server address and either --%s or --%s",
			option.AdminFlag, option.SSOFlag,
		)
	}
	return nil
}

// run logs in to the Kargo API server using the method specified by the
// options.
func (o *loginOptions) run(ctx context.Context) error {
	var bearerToken, refreshToken string
	var err error

	var authMethod string
	switch {
	case o.UseAdmin:
		authMethod = authMethodAdmin
		for o.Password == "" {

			prompt := &survey.Password{
				Message: "Admin user password",
			}
			if err = survey.AskOne(
				prompt,
				&o.Password,
				io.SurveyStdio(o.IOStreams),
			); err != nil {
				return err
			}
		}
		if bearerToken, err = adminLogin(ctx, o.ServerAddress, o.Password, o.InsecureTLS); err != nil {
			return err
		}
	case o.UseSSO:
		authMethod = authMethodSSO
		if bearerToken, refreshToken, err = o.ssoLogin(ctx); err != nil {
			return err
		}
	default:
		// This should never happen.
		return errors.New("internal error: no login method selected")
	}

	if o.InsecureTLS {
		// When the user specifies during login that they want to ignore cert
		// warnings, we will force them to periodically re-assess that choice
		// by NOT using refresh tokens and requiring them to re-authenticate
		// instead. Since we plan not to use the refresh token for such a case,
		// it's more secure to throw it away immediately.
		refreshToken = ""
	}

	if o.Config.APIAddress != o.ServerAddress {
		o.Config = libConfig.NewDefaultCLIConfig()
	}

	o.Config.APIAddress = o.ServerAddress
	o.Config.AuthMethod = authMethod
	o.Config.CallbackPort = o.CallbackPort
	o.Config.UseDeviceCode = o.UseDeviceCode
	o.Config.BearerToken = bearerToken
	o.Config.RefreshToken = refreshToken
	o.Config.InsecureSkipTLSVerify = o.InsecureTLS

	if err = libConfig.SaveCLIConfig(o.Config); err != nil {
		return fmt.Errorf("error persisting configuration: %w", err)
	}
	return nil
}

func adminLogin(
	ctx context.Context,
	serverAddress string,
	password string,
	insecureTLS bool,
) (string, error) {
	apiClient, err := client.GetClient(serverAddress, "", insecureTLS)
	if err != nil {
		return "", err
	}
	cfg, httpRes, err := apiClient.SystemAPI.GetPublicConfig(ctx).Execute()
	if httpRes != nil {
		defer httpRes.Body.Close()
	}
	if err != nil {
		return "", fmt.Errorf(
			"error retrieving public configuration from server: %w",
			client.APIError(err),
		)
	}

	if !cfg.GetAdminAccountEnabled() {
		return "", errors.New("server does not support admin user login")
	}

	if apiClient, err = client.GetClient(
		serverAddress,
		password,
		insecureTLS,
	); err != nil {
		return "", err
	}

	loginRes, httpRes, err := apiClient.SystemAPI.AdminLogin(ctx).Execute()
	if httpRes != nil {
		defer httpRes.Body.Close()
	}
	if err != nil {
		return "", fmt.Errorf(
			"error logging in as admin user: %w",
			client.APIError(err),
		)
	}

	return loginRes.GetIdToken(), nil
}

// ssoLogin performs a login using OpenID Connect. It first retrieves
// non-sensitive configuration from the Kargo API server, then uses that
// configuration to authenticate the user with the identity provider specified
// by the API server. If o.UseDeviceCode is true, it uses the device
// authorization grant. Otherwise, it uses an authorization code flow with PKCE.
// Upon success, it returns the ID token and refresh token.
func (o *loginOptions) ssoLogin(ctx context.Context) (string, string, error) {
	apiClient, err := client.GetClient(o.ServerAddress, "", o.InsecureTLS)
	if err != nil {
		return "", "", err
	}
	cfg, httpRes, err := apiClient.SystemAPI.GetPublicConfig(ctx).Execute()
	if httpRes != nil {
		defer httpRes.Body.Close()
	}
	if err != nil {
		return "", "", fmt.Errorf(
			"error retrieving public configuration from server: %w",
			client.APIError(err),
		)
	}

	if !cfg.HasOidcConfig() {
		return "", "", errors.New("server does not support OpenID Connect")
	}
	svrCfg := cfg.GetOidcConfig()

	scopes := svrCfg.GetScopes()

	httpClient := cleanhttp.DefaultClient()
	if o.InsecureTLS {
		transport := cleanhttp.DefaultTransport()
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // nolint: gosec
		}
		httpClient.Transport = transport
	}
	ctx = oidc.ClientContext(ctx, httpClient)

	provider, err := oidc.NewProvider(ctx, svrCfg.GetIssuerUrl())
	if err != nil {
		return "", "", fmt.Errorf("error initializing OIDC provider: %w", err)
	}

	providerClaims := struct {
		ScopesSupported []string `json:"scopes_supported"`
	}{}
	if err = provider.Claims(&providerClaims); err != nil {
		return "", "", fmt.Errorf("error retrieving provider claims: %w", err)
	}
	const offlineAccessScope = "offline_access"
	// If the provider supports the "offline_access" scope, request it so that
	// we can get a refresh token.
	if slices.Contains(providerClaims.ScopesSupported, offlineAccessScope) {
		scopes = append(scopes, offlineAccessScope)
	}

	oauthCfg := oauth2.Config{
		ClientID: svrCfg.GetClientId(),
		Endpoint: provider.Endpoint(),
		Scopes:   scopes,
	}
	if svrCfg.GetCliClientId() != "" {
		// There is an OIDC client ID specifically meant for CLI use
		oauthCfg.ClientID = svrCfg.GetCliClientId()
	}

	var token *oauth2.Token
	if o.UseDeviceCode {
		token, err = o.deviceCodeLogin(ctx, oauthCfg)
	} else {
		token, err = o.authCodeLogin(ctx, oauthCfg, browser.OpenURL)
	}
	if err != nil {
		return "", "", err
	}

	idToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", errors.New("no id_token in token response")
	}

	return idToken, token.RefreshToken, nil
}

// authCodeLogin performs an authorization code flow with PKCE. It writes the
// identity provider's authorization URL to o.ErrOut and attempts to open it
// with openBrowser, then receives the authorization code via a callback to a
// temporary web server listening on localhost:o.CallbackPort. A CallbackPort
// of 0 selects any available port. Upon success, it returns the token
// response.
func (o *loginOptions) authCodeLogin(
	ctx context.Context,
	oauthCfg oauth2.Config,
	openBrowser func(string) error,
) (*oauth2.Token, error) {
	listener, err := net.Listen(
		"tcp",
		fmt.Sprintf("localhost:%d", o.CallbackPort),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating callback listener: %w", err)
	}

	oauthCfg.RedirectURL = fmt.Sprintf(
		"http://localhost:%s/auth/callback",
		strings.Split(listener.Addr().String(), ":")[1],
	)

	// Per the spec, this must be guessable with probability <= 2^(-128). The
	// following call generates one of 52^24 random strings, ~= 2^136
	// possibilities.
	//
	// See: https://www.rfc-editor.org/rfc/rfc6749#section-10.10
	state, err := randString(24)
	if err != nil {
		return nil, fmt.Errorf("error generating state: %w", err)
	}

	codeCh := make(chan string)
	errCh := make(chan error)
	go receiveAuthCode(ctx, listener, state, codeCh, errCh)

	codeVerifier, codeChallenge, err := createPCKEVerifierAndChallenge()
	if err != nil {
		return nil, fmt.Errorf("error creating PCKE code verifier and code challenge: %w", err)
	}
	u := oauthCfg.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	_, _ = fmt.Fprintf(
		o.ErrOut,
		"To log in, open the following URL in a web browser on this machine if "+
			"one doesn't open automatically:\n\n%s\n\n",
		u,
	)
	// The user can open the URL themselves, so failing to open a browser is not
	// an error.
	_ = openBrowser(u)

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, fmt.Errorf("error in callback handler: %w", err)
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for user to complete authentication")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	token, err := oauthCfg.Exchange(
		ctx,
		code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("error exchanging auth code for token: %w", err)
	}

	// Slight delay to allow all assets used by the splash page to be served up
	<-time.After(2 * time.Second)

	return token, nil
}

// receiveAuthCode runs a web server that serves the callback endpoint for
// receiving the authorization code at the end of an authorization code flow.
// It returns the authorization code or any error that occurs via the provided
// channels.
func receiveAuthCode(
	ctx context.Context,
	listener net.Listener,
	state string,
	codeCh chan<- string,
	errCh chan<- error,
) {
	mux := http.NewServeMux()

	mux.Handle("/assets/", http.FileServer(http.FS(assets)))

	mux.HandleFunc(
		"/auth/callback",
		func(w http.ResponseWriter, r *http.Request) {
			callbackState := r.FormValue("state")
			if callbackState == "" {
				callbackState = r.URL.Query().Get("state")
			}
			if callbackState == "" {
				select {
				case errCh <- errors.New("no state found in callback request"):
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if callbackState != state {
				select {
				case errCh <- errors.New("unrecognized state in callback request"):
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			code := r.FormValue("code")
			if code == "" {
				code = r.URL.Query().Get("code")
			}
			if code == "" {
				select {
				case errCh <- errors.New("no code found in callback request"):
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			select {
			case codeCh <- code:
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(splashHTML)
			case <-r.Context().Done():
				w.WriteHeader(http.StatusInternalServerError)
			}

		},
	)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Minute,
	}
	if err := srv.Serve(listener); err != nil {
		select {
		case errCh <- fmt.Errorf("error running temporary HTTP server: %w", err):
		case <-ctx.Done():
		}
	}
}

// deviceCodeLogin performs a device authorization grant. It requests a device
// code from the identity provider, writes instructions for the user to
// o.ErrOut, and polls the identity provider until the user completes
// authentication in a web browser on any device or the device code expires.
// Upon success, it returns the token response.
func (o *loginOptions) deviceCodeLogin(
	ctx context.Context,
	oauthCfg oauth2.Config,
) (*oauth2.Token, error) {
	if oauthCfg.Endpoint.DeviceAuthURL == "" {
		return nil, errors.New(
			"identity provider does not support the device authorization grant",
		)
	}

	deviceAuth, err := oauthCfg.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("error requesting device authorization: %w", err)
	}

	o.writeDeviceAuthInstructions(deviceAuth)

	token, err := oauthCfg.DeviceAccessToken(ctx, deviceAuth)
	if err != nil {
		// DeviceAccessToken stops polling when the device code expires. The
		// caller's own context ending is reported as is.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, errors.New(
				"timed out waiting for user to complete authentication",
			)
		}
		return nil, fmt.Errorf("error waiting for device authorization: %w", err)
	}

	return token, nil
}

// writeDeviceAuthInstructions writes instructions for completing a device
// authorization grant to o.ErrOut. The user code is always shown. When the
// identity provider supplies a verification URL that already includes the user
// code, the user is asked to confirm the code rather than enter it.
func (o *loginOptions) writeDeviceAuthInstructions(
	deviceAuth *oauth2.DeviceAuthResponse,
) {
	if deviceAuth.VerificationURIComplete != "" {
		_, _ = fmt.Fprintf(
			o.ErrOut,
			"To log in, open the following URL in a web browser on any device "+
				"and confirm that it shows the code %s:\n\n%s\n\n",
			deviceAuth.UserCode, deviceAuth.VerificationURIComplete,
		)
		return
	}
	_, _ = fmt.Fprintf(
		o.ErrOut,
		"To log in, open the following URL in a web browser on any device "+
			"and enter the code %s:\n\n%s\n\n",
		deviceAuth.UserCode, deviceAuth.VerificationURI,
	)
}

// createPCKEVerifierAndChallenge creates a PKCE code verifier and code
// challenge. The relationship between the two is that the code challenge is the
// base64 URL-encoded SHA256 hash of the code verifier. The returned code
// challenge can be sent to an identity provider at the start of an
// authorization code flow with PKCE, while the returned code verifier can be
// used to exchange the authorization code from the identity provider for a
// user's identity token and/or an access token.
func createPCKEVerifierAndChallenge() (string, string, error) {
	codeVerifier, err := randStringFromCharset(
		// This is the max length allowed by the spec.
		//
		// See: https://www.rfc-editor.org/rfc/rfc7636#section-4.1
		128,
		// These are the unreserved characters specified in the spec.
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~",
	)
	if err != nil {
		return "", "", fmt.Errorf("error creating PKCE code verifier: %w", err)
	}
	codeChallengeHash := sha256.Sum256([]byte(codeVerifier))
	// [:] converts [32]byte to []byte
	codeChallenge := base64.RawURLEncoding.EncodeToString(codeChallengeHash[:])
	return codeVerifier, codeChallenge, nil
}

// randString generates a random string of the specified length using only
// characters from a default character set.
func randString(n int) (string, error) {
	return randStringFromCharset(n, defaultRandStringCharSet)
}

// randStringFromCharset generates a random string of the specified length using
// only characters from the specified character set.
func randStringFromCharset(n int, charset string) (string, error) {
	b := make([]byte, n)
	maxIdx := big.NewInt(int64(len(charset)))
	for i := 0; i < n; i++ {
		randIdx, err := rand.Int(rand.Reader, maxIdx)
		if err != nil {
			return "", fmt.Errorf("failed to generate random string: %w", err)
		}
		// randIdx is necessarily safe to convert to int, because the max came from
		// an int
		randIdxInt := int(randIdx.Int64())
		b[i] = charset[randIdxInt]
	}
	return string(b), nil
}

// normalizeURL ensures a given address has a scheme, defaulting to "https".
func normalizeURL(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return address
	}

	// If no scheme is present, prepend "https://"
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}

	// Parse to normalize
	parsedURL, err := url.Parse(address)
	if err != nil {
		return address // Return the best attempt if parsing fails
	}
	return parsedURL.String()
}

var splashHTML = []byte(`<!DOCTYPE html>
<html>
<head>
  <meta charset='utf-8'>
  <meta http-equiv='X-UA-Compatible' content='IE=edge'>
  <meta name='viewport' content='width=device-width, initial-scale=1'>
  <title>Kargo</title>
  <link rel="shortcut icon" type="image/png" href="/assets/kargo.png"/>
  <link rel='stylesheet' type='text/css' media='screen' href='/assets/splash.css'>
</head>
<body>
  <div class="splash">
    <img src="/assets/kargo.png" alt="Kargo" />
    <p>You are now logged in and may resume using the Kargo CLI.</p>
  </div>
</body>
</html>
`)
