package get

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	v1alpha1 "github.com/akuity/kargo/api/service/v1alpha1"
	"github.com/akuity/kargo/pkg/cli/client"
	"github.com/akuity/kargo/pkg/cli/config"
	"github.com/akuity/kargo/pkg/cli/io"
	"github.com/akuity/kargo/pkg/cli/option"
	"github.com/akuity/kargo/pkg/cli/templates"
)

type getServiceAccountTokenOptions struct {
	genericiooptions.IOStreams

	Config        config.CLIConfig
	ClientOptions client.Options

	Project string
	Name    string
}

func newGetServiceAccountTokenCommand(
	cfg config.CLIConfig,
	streams genericiooptions.IOStreams,
) *cobra.Command {
	cmdOpts := &getServiceAccountTokenOptions{
		Config:    cfg,
		IOStreams: streams,
	}

	cmd := &cobra.Command{
		Use:     "serviceaccount-token [--project=project] NAME",
		Aliases: []string{"sa-token"},
		Short: "Generate and retrieve a token for the service account; revokes " +
			"any existing token",
		Args: option.ExactArgs(1),
		Example: templates.Example(`
# Get the token for service account my-service-account in my-project
kargo get serviceaccount-token --project=my-project my-service-account

# Get the token for service account my-service-account in the default project
kargo config set-project my-project
kargo get serviceaccount-token my-service-account
`),
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

// addFlags adds the flags for the get service account token options to the
// provided command.
func (o *getServiceAccountTokenOptions) addFlags(cmd *cobra.Command) {
	o.ClientOptions.AddFlags(cmd.PersistentFlags())

	option.Project(
		cmd.Flags(), &o.Project, o.Config.Project,
		"The project for which to get a service account's token. If not set, the "+
			"default project will be used.",
	)
}

// complete sets the options from the command arguments.
func (o *getServiceAccountTokenOptions) complete(args []string) {
	o.Name = strings.TrimSpace(args[0])
}

// validate performs validation of the options. If the options are invalid, an
// error is returned.
func (o *getServiceAccountTokenOptions) validate() error {
	// While the flags are marked as required, a user could still provide an empty
	// string. This is a check to ensure that the flags are not empty.
	if o.Project == "" {
		return fmt.Errorf("%s is required", option.ProjectFlag)
	}
	return nil
}

// run gets the the service account's token from the server and prints it to the
// console.
func (o *getServiceAccountTokenOptions) run(ctx context.Context) error {
	kargoSvcCli, err := client.GetClientFromConfig(ctx, o.Config, o.ClientOptions)
	if err != nil {
		return fmt.Errorf("get client from config: %w", err)
	}

	resp, err := kargoSvcCli.GetNewServiceAccountToken(
		ctx,
		connect.NewRequest(
			&v1alpha1.GetNewServiceAccountTokenRequest{
				Project: o.Project,
				Name:    o.Name,
			},
		),
	)
	if err != nil {
		return fmt.Errorf("get service account token: %w", err)
	}

	fmt.Fprintln(o.Out, resp.Msg.Token)

	return nil
}
