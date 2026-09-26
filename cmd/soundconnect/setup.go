package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

func newSetupCommand(deps app.Deps) *cobra.Command {
	var request app.SetupRequest
	var backendValue, socksListen, upstreamProxy string
	var tlsInsecure, nativeTLSInsecure bool
	command := &cobra.Command{
		Use: "setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Flags left out keep the saved settings.
			flags := cmd.Flags()
			if flags.Changed("backend") {
				request.Backend = backendValue
			}
			if flags.Changed("socks-listen") {
				request.SOCKSListen = &socksListen
			}
			if flags.Changed("upstream-proxy") {
				request.UpstreamProxy = &upstreamProxy
			}
			if flags.Changed("tls-insecure") {
				request.TLSInsecure = &tlsInsecure
			}
			if flags.Changed("native-tls-insecure") {
				request.NativeTLSInsecure = &nativeTLSInsecure
			}
			// A terminal user gets the guided wizard; piped input keeps the
			// line-prompt contract the macOS app drives.
			deps := withInteraction(deps, app.LineOptions{PasswordFromStdin: request.PasswordSupplied}, cmd.ErrOrStderr())
			request.Guided = deps.Interactive
			ctx := cmd.Context()
			result, err := app.Setup(ctx, deps, request)
			if err != nil {
				return err
			}
			stdout := cmd.OutOrStdout()
			fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\ncredential: %s\n", result.Path, result.Backend, result.Credential)
			if !deps.Interactive || result.Backend != backend.EasyConnect {
				return nil
			}
			// aTrust sign-in needs the full connect flow, so only EasyConnect
			// offers a quick check.
			test, err := deps.Interaction.Confirm(ctx, "Test the login now?", true)
			if err != nil || !test {
				return err
			}
			return dryRun(ctx, deps, stdout)
		},
	}
	flags := command.Flags()
	flags.StringVar(&backendValue, "backend", string(backend.EasyConnect), "protocol backend (easyconnect or atrust)")
	flags.StringVar(&request.Server, "server", "", "campus VPN gateway host or host:port")
	flags.StringVar(&request.Username, "username", "", "campus account")
	flags.StringVar(&request.AuthType, "auth-type", "", "aTrust authentication type (auth/httpsOauth2 or auth/psw)")
	flags.StringVar(&request.LoginDomain, "login-domain", "", "aTrust login domain override")
	flags.StringVar(&socksListen, "socks-listen", config.DefaultSOCKSListen, "numeric loopback SOCKS5 listener")
	flags.StringVar(&upstreamProxy, "upstream-proxy", "", "optional socks5 upstream URL")
	flags.BoolVar(&tlsInsecure, "tls-insecure", false, "allow an unverified development gateway certificate")
	flags.BoolVar(&nativeTLSInsecure, "native-tls-insecure", false, "disable verification only for native protocol TLS")
	flags.BoolVar(&request.PasswordSupplied, "password-stdin", false, "read the password from standard input without a terminal prompt")
	return command
}

func newMigrateCommand(deps app.Deps) *cobra.Command {
	var legacyRoot string
	command := &cobra.Command{
		Use: "migrate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := app.Migrate(withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()), legacyRoot)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configuration_migrated: %t\ncredential_migrated: %t\nsource_preserved: true\n",
				result.ConfigurationMigrated, result.CredentialMigrated)
			return nil
		},
	}
	command.Flags().StringVar(&legacyRoot, "from", ".", "directory containing the legacy .config state")
	return command
}
