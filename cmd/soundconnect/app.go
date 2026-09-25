package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/soundadam/soundconnect/internal/app"
)

// commandDeps wires the production dependencies for one command. The
// package-level factories stay swappable for tests until the connect
// lifecycle moves into internal/app too.
func commandDeps(interaction app.Interaction, stderr io.Writer) app.Deps {
	if interaction == nil {
		interaction = app.NewLineInteraction(app.LineOptions{Input: os.Stdin, Output: stderr})
	}
	return app.Deps{
		Paths:              resolveDefaultPaths,
		PasswordStore:      newSystemCredentialStore,
		ATrustSessionStore: newATrustClientDataStore,
		ATrustCore:         newATrustCore,
		OAuthHelper:        atrustOAuthHelperPath,
		Interaction:        interaction,
		Diagnostics:        stderr,
	}
}

// exitCode is an error that only carries an exit status: whatever the user
// needs to see has already been written.
type exitCode int

func (code exitCode) Error() string { return fmt.Sprintf("exit status %d", int(code)) }

// exitStatus is the single mapping from a command's error to its exit status
// and stderr message: nil → 0, cancellation → 0, app.UsageError → 2,
// anything else → 1.
func exitStatus(err error, stderr io.Writer) int {
	var code exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &code):
		return int(code)
	case errors.Is(err, context.Canceled):
		return 0
	case app.IsUsage(err):
		fmt.Fprintln(stderr, err)
		return 2
	default:
		fmt.Fprintln(stderr, err)
		return 1
	}
}

// parseCommand parses flags for a command that takes no positional
// arguments. Help output goes to stdout with status 0; parse errors are
// written to stderr with status 2.
func parseCommand(flags *flag.FlagSet, arguments []string, stdout, stderr io.Writer) error {
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return exitCode(code)
	}
	if flags.NArg() != 0 {
		return app.Usagef("%s accepts no positional arguments", strings.TrimPrefix(flags.Name(), "soundconnect "))
	}
	return nil
}
