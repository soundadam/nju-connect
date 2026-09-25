package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/app"
)

func runBackends(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect backends", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "write machine-readable backend metadata")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	catalog := app.Backends()
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(catalog); err != nil {
			return fmt.Errorf("encode backend metadata: %w", err)
		}
		return nil
	}
	for _, descriptor := range catalog.Backends {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", descriptor.ID, descriptor.DisplayName, descriptor.DefaultGateway)
	}
	return nil
}
