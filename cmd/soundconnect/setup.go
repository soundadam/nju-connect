package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

func runSetup(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect setup", flag.ContinueOnError)
	var request app.SetupRequest
	backendValue := flags.String("backend", string(backend.EasyConnect), "protocol backend (easyconnect or atrust)")
	flags.StringVar(&request.Server, "server", "", "campus VPN gateway host or host:port")
	flags.StringVar(&request.Username, "username", "", "campus account")
	flags.StringVar(&request.AuthType, "auth-type", "", "aTrust authentication type (auth/httpsOauth2 or auth/psw)")
	flags.StringVar(&request.LoginDomain, "login-domain", "", "aTrust login domain override")
	socksListen := flags.String("socks-listen", config.DefaultSOCKSListen, "numeric loopback SOCKS5 listener")
	upstreamProxy := flags.String("upstream-proxy", "", "optional socks5 upstream URL")
	tlsInsecure := flags.Bool("tls-insecure", false, "allow an unverified development gateway certificate")
	nativeTLSInsecure := flags.Bool("native-tls-insecure", false, "disable verification only for native protocol TLS")
	flags.BoolVar(&request.PasswordSupplied, "password-stdin", false, "read the password from standard input without a terminal prompt")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	// Flags left out keep the saved settings.
	flags.Visit(func(set *flag.Flag) {
		switch set.Name {
		case "backend":
			request.Backend = *backendValue
		case "socks-listen":
			request.SOCKSListen = socksListen
		case "upstream-proxy":
			request.UpstreamProxy = upstreamProxy
		case "tls-insecure":
			request.TLSInsecure = tlsInsecure
		case "native-tls-insecure":
			request.NativeTLSInsecure = nativeTLSInsecure
		}
	})
	interaction := app.NewLineInteraction(app.LineOptions{
		Input: os.Stdin, Output: stderr, PasswordFromStdin: request.PasswordSupplied,
	})
	result, err := app.Setup(context.Background(), commandDeps(interaction, stderr), request)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\ncredential: %s\n", result.Path, result.Backend, result.Credential)
	return nil
}

func runMigrate(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect migrate", flag.ContinueOnError)
	legacyRoot := flags.String("from", ".", "directory containing the legacy .config state")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	result, err := app.Migrate(commandDeps(nil, stderr), *legacyRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "configuration_migrated: %t\ncredential_migrated: %t\nsource_preserved: true\n",
		result.ConfigurationMigrated, result.CredentialMigrated)
	return nil
}
