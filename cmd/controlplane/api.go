package main

import (
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"

	"github.com/akuity/kargo/internal/api"
)

type apiOptions struct {
	Host string
	Port string
}

func newAPICommand() *cobra.Command {
	cmdOpts := &apiOptions{}

	cmd := &cobra.Command{
		Use:               "api",
		DisableAutoGenTag: true,
		SilenceErrors:     true,
		SilenceUsage:      true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmdOpts.complete()

			return cmdOpts.run()
		},
	}

	return cmd
}

func (o *apiOptions) complete() {
	o.Host = os.Getenv("HOST")
	if o.Host == "" {
		o.Host = "0.0.0.0"
	}
	o.Port = os.Getenv("PORT")
	if o.Port == "" {
		o.Port = "8080"
	}
}

func (o *apiOptions) run() error {
	srv := api.NewServer()
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%s", o.Host, o.Port))
	if err != nil {
		return fmt.Errorf("error creating listener: %w", err)
	}
	defer l.Close()
	if err = srv.Serve(l); err != nil {
		return fmt.Errorf("error serving API: %w", err)
	}
	return nil
}
