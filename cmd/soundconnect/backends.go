package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/soundadam/soundconnect/internal/app"
)

func newBackendsCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "backends",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout := cmd.OutOrStdout()
			catalog := app.Backends()
			if asJSON {
				if err := json.NewEncoder(stdout).Encode(catalog); err != nil {
					return fmt.Errorf("encode backend metadata: %w", err)
				}
				return nil
			}
			for _, descriptor := range catalog.Backends {
				fmt.Fprintf(stdout, "%s\t%s\t%s\n", descriptor.ID, descriptor.DisplayName, descriptor.DefaultGateway)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "write machine-readable backend metadata")
	return command
}
