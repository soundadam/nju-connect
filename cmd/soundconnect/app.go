package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/soundadam/soundconnect/internal/backend/atrust"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/speedtest"
	"github.com/soundadam/soundconnect/internal/tui"
)

// productionDeps are the real side effects: the user's state directory,
// the system keyring, the linked aTrust core, and this process's stdin.
func productionDeps() app.Deps {
	return app.Deps{
		Paths:              config.DefaultPaths,
		PasswordStore:      openCredentialStore,
		ATrustSessionStore: openCredentialStore,
		ATrustCore:         atrustbackend.NewCore,
		OAuthHelper:        app.OAuthHelperPath,
		EasyConnectSession: app.NewEasyConnectSession,
		StartBackground:    app.StartBackground,
		Speedtest: app.SpeedtestDeps{
			Asset:        speedtest.DefaultComponentAsset,
			ExternalPath: externalSpeedtestHelperPath,
			HTTPClient:   func() *http.Client { return nil },
			Probe:        speedtest.ProbeReachability,
		},
		Stdin: os.Stdin,
	}
}

func openCredentialStore(location credential.Location) (credential.Store, error) {
	return credential.Open(location)
}

// withInteraction picks how a command asks questions: forms for a person at
// a terminal and line prompts otherwise, with prompts and helper output on
// stderr. Tests that pin deps.Interaction keep it.
func withInteraction(deps app.Deps, options app.LineOptions, stderr io.Writer) app.Deps {
	deps.Diagnostics = stderr
	if deps.Interaction != nil {
		return deps
	}
	options.Input = deps.Stdin
	deps.Interaction, deps.Interactive = tui.ForCommand(options, stderr)
	return deps
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
