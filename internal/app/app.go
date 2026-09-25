// Package app holds the CLI's use cases. Each service takes its dependencies
// explicitly through Deps, asks the user for input only through Interaction,
// and returns a result struct or an error. Rendering text or JSON and mapping
// errors to exit codes belong to the command layer in cmd/soundconnect.
package app

import (
	"errors"
	"fmt"
	"io"

	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

// Deps are the side effects a service may use. Tests build their own.
type Deps struct {
	// Paths resolves the state directory: SOUNDCONNECT_CONFIG_DIR, or the
	// user configuration directory.
	Paths func() (config.Paths, error)
	// PasswordStore opens the long-lived VPN password shared by both backends.
	PasswordStore func(path string) (credential.Store, error)
	// ATrustSessionStore opens the saved aTrust client data.
	ATrustSessionStore func(path string) (credential.Store, error)
	// ATrustCore constructs the aTrust protocol core linked into this build.
	ATrustCore func() atrustbackend.Core
	// OAuthHelper locates the bundled aTrust OAuth helper, if any.
	OAuthHelper func() (string, bool)
	// Interaction asks the user for input.
	Interaction Interaction
	// Diagnostics receives helper-process output meant for the user. The
	// command layer points it at stderr so stdout stays machine-readable.
	Diagnostics io.Writer
}

func (deps Deps) diagnostics() io.Writer {
	if deps.Diagnostics == nil {
		return io.Discard
	}
	return deps.Diagnostics
}

// UsageError reports invalid arguments. The command layer maps it to exit
// status 2; every other error except cancellation maps to 1.
type UsageError struct {
	err error
}

// Usagef formats a UsageError. Like fmt.Errorf, %w wraps a cause.
func Usagef(format string, arguments ...any) error {
	return &UsageError{err: fmt.Errorf(format, arguments...)}
}

func (usage *UsageError) Error() string { return usage.err.Error() }

func (usage *UsageError) Unwrap() error { return usage.err }

// IsUsage reports whether err is, or wraps, a UsageError.
func IsUsage(err error) bool {
	var usage *UsageError
	return errors.As(err, &usage)
}
