package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/app"
)

func runLogout(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect logout", flag.ContinueOnError)
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	result, err := app.Logout(context.Background(), withInteraction(deps, app.LineOptions{}, stderr))
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "atrust_session_cleared: true")
	fmt.Fprintf(stdout, "oauth_profile_cleared: %t\n", result.OAuthProfileCleared)
	return nil
}
