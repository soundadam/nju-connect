package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

type backendCatalogResponse struct {
	SchemaVersion int                  `json:"schema_version"`
	SOCKSListen   string               `json:"socks_listen"`
	Backends      []backend.Descriptor `json:"backends"`
}

func runBackends(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect backends", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "write machine-readable backend metadata")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "backends accepts no positional arguments")
		return 2
	}

	response := backendCatalogResponse{
		SchemaVersion: 1,
		SOCKSListen:   config.DefaultSOCKSListen,
		Backends:      backend.Catalog(),
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(response); err != nil {
			fmt.Fprintf(stderr, "encode backend metadata: %v\n", err)
			return 1
		}
		return 0
	}
	for _, descriptor := range response.Backends {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", descriptor.ID, descriptor.DisplayName, descriptor.DefaultGateway)
	}
	return 0
}
