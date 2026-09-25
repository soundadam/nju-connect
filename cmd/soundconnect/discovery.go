package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
)

func runAuthInfo(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect auth-info", flag.ContinueOnError)
	backendValue := flags.String("backend", string(backend.ATrust), "protocol backend")
	server := flags.String("server", backend.DefaultATrustGateway, "aTrust gateway host or host:port")
	asJSON := flags.Bool("json", false, "write machine-readable authentication methods")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	methods, err := app.DiscoverATrust(context.Background(), commandDeps(nil, stderr), *backendValue, *server)
	if err != nil {
		return err
	}
	if *asJSON {
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
}

func runDoctor(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect doctor", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	report, err := app.Doctor(commandDeps(nil, stderr))
	if err != nil {
		return err
	}
	if *asJSON {
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
}
