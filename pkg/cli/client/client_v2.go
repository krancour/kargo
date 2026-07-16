package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/go-cleanhttp"

	kargogen "github.com/akuity/kargo-spike"
	"github.com/akuity/kargo/pkg/cli/config"
)

// SPIKE: This file wires up the openapi-generator-generated client
// (hack/testing/openapi-generator-spike/generated) as the eventual
// replacement for the go-swagger-generated client wired up in client.go.
// It exists to prove the migration out on a small slice of the CLI. See
// hack/testing/openapi-generator-spike/FINDINGS.md.

// GetClientV2FromConfig returns a client for the Kargo API server located at
// the address specified in local configuration, using credentials also
// specified in the local configuration.
func GetClientV2FromConfig(
	ctx context.Context,
	cfg config.CLIConfig,
	opts Options,
) (*kargogen.APIClient, error) {
	if cfg.APIAddress == "" || cfg.BearerToken == "" {
		return nil, errNotLoggedIn
	}
	skipTLSVerify := opts.InsecureTLS || cfg.InsecureSkipTLSVerify
	cfg, err := newTokenRefresher().refreshToken(ctx, cfg, skipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("error refreshing token: %w", err)
	}
	return GetClientV2(cfg.APIAddress, cfg.BearerToken, skipTLSVerify)
}

// GetClientV2 returns a client for the Kargo API server located at the
// specified address, authenticating with the specified credential if one is
// provided.
func GetClientV2(
	serverAddress string,
	credential string,
	insecureTLS bool,
) (*kargogen.APIClient, error) {
	if _, err := url.Parse(serverAddress); err != nil {
		return nil, fmt.Errorf("invalid server address: %w", err)
	}

	baseTransport := cleanhttp.DefaultTransport()
	if insecureTLS {
		baseTransport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // nolint: gosec
		}
	}

	genCfg := kargogen.NewConfiguration()
	genCfg.Servers = kargogen.ServerConfigurations{
		{URL: strings.TrimSuffix(serverAddress, "/")},
	}
	genCfg.HTTPClient = &http.Client{
		Transport: &versionHeaderTransport{wrapped: baseTransport},
	}
	if credential != "" {
		genCfg.AddDefaultHeader("Authorization", "Bearer "+credential)
	}
	return kargogen.NewAPIClient(genCfg), nil
}

// V2APIError makes API errors returned by the v2 client presentable. The
// client's GenericOpenAPIError renders only the HTTP status text; the
// server's explanation is in the response body it carries.
func V2APIError(err error) error {
	genErr := &kargogen.GenericOpenAPIError{}
	if errors.As(err, &genErr) {
		if body := strings.TrimSpace(string(genErr.Body())); body != "" {
			return fmt.Errorf("%s: %s", genErr.Error(), body)
		}
	}
	return err
}
