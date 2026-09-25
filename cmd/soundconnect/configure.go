package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
)

// runConfigure changes only non-secret connection settings. The macOS host
// adapter uses this command when a user switches between backends.
func runConfigure(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect configure", flag.ContinueOnError)
	var request app.ConfigureRequest
	flags.StringVar(&request.Backend, "backend", "", "protocol backend (easyconnect or atrust)")
	flags.StringVar(&request.Server, "server", "", "campus VPN gateway host or host:port")
	flags.StringVar(&request.Username, "username", "", "campus account (preserves the saved account when omitted)")
	flags.StringVar(&request.AuthType, "auth-type", "", "aTrust authentication type")
	flags.StringVar(&request.LoginDomain, "login-domain", "", "aTrust login domain")
	flags.StringVar(&request.SOCKSListen, "socks-listen", "", "numeric loopback SOCKS5 listener")
	flags.StringVar(&request.UpstreamProxy, "upstream-proxy", "", "optional socks5 upstream URL")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	result, err := app.Configure(withInteraction(deps, app.LineOptions{}, stderr), request)
	if err != nil {
		return err
	}
	configured := result.Config
	fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\nserver: %s\nsocks_listen: %s\n",
		result.Path, configured.BackendName(), configured.Server, configured.SOCKSListen)
	if configured.BackendName() == backend.ATrust {
		fmt.Fprintf(stdout, "auth_type: %s\nlogin_domain: %s\n", configured.AuthType, configured.LoginDomain)
	}
	return nil
}
