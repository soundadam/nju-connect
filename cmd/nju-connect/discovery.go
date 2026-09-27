package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/soundadam/nju-connect/internal/app"
	"github.com/soundadam/nju-connect/internal/backend"
)

func newAuthInfoCommand(deps app.Deps) *cobra.Command {
	var backendValue, server string
	var asJSON bool
	command := &cobra.Command{
		Use: "auth-info",
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr())
			methods, err := app.DiscoverATrust(cmd.Context(), deps, backendValue, server)
			if err != nil {
				return err
			}
			stdout := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(stdout).Encode(methods); err != nil {
					return fmt.Errorf("encode authentication methods: %w", err)
				}
				return nil
			}
			for _, method := range methods {
				fmt.Fprintf(stdout, "backend: %s\nauth_name: %s\nauth_type: %s\nlogin_domain: %s\n",
					backend.ATrust, method.Name, method.Type, method.Domain)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&backendValue, "backend", string(backend.ATrust), "protocol backend")
	flags.StringVar(&server, "server", backend.DefaultATrustGateway, "aTrust gateway host or host:port")
	flags.BoolVar(&asJSON, "json", false, "write machine-readable authentication methods")
	return command
}

func newDoctorCommand(deps app.Deps) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "doctor",
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := app.Doctor(withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			stdout := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(stdout).Encode(report); err != nil {
					return fmt.Errorf("encode report: %w", err)
				}
			} else {
				fmt.Fprintf(stdout, "ready: %t\nconfiguration: %s\ncredential_store: %s\nupstream_proxy: %s\nnext: %s\n",
					report.Ready, report.Configuration, report.CredentialStore, report.UpstreamProxy, report.NextStep)
			}
			if !report.Ready {
				return exitCode(1)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return command
}
