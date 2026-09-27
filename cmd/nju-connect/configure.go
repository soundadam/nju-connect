package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/soundadam/nju-connect/internal/app"
	"github.com/soundadam/nju-connect/internal/backend"
)

// newConfigureCommand changes only non-secret connection settings. The
// macOS host adapter uses this command when a user switches between
// backends.
func newConfigureCommand(deps app.Deps) *cobra.Command {
	var request app.ConfigureRequest
	command := &cobra.Command{
		Use: "configure",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := app.Configure(withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()), request)
			if err != nil {
				return err
			}
			stdout := cmd.OutOrStdout()
			configured := result.Config
			fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\nserver: %s\nsocks_listen: %s\n",
				result.Path, configured.BackendName(), configured.Server, configured.SOCKSListen)
			if configured.BackendName() == backend.ATrust {
				fmt.Fprintf(stdout, "auth_type: %s\nlogin_domain: %s\n", configured.AuthType, configured.LoginDomain)
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVar(&request.Backend, "backend", "", "protocol backend (easyconnect or atrust)")
	flags.StringVar(&request.Server, "server", "", "campus VPN gateway host or host:port")
	flags.StringVar(&request.Username, "username", "", "campus account (preserves the saved account when omitted)")
	flags.StringVar(&request.AuthType, "auth-type", "", "aTrust authentication type")
	flags.StringVar(&request.LoginDomain, "login-domain", "", "aTrust login domain")
	flags.StringVar(&request.SOCKSListen, "socks-listen", "", "numeric loopback SOCKS5 listener")
	flags.StringVar(&request.UpstreamProxy, "upstream-proxy", "", "optional socks5 upstream URL")
	return command
}
