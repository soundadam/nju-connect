// Package app holds the CLI's use cases. Each service takes its dependencies
// explicitly through Deps, asks the user for input only through Interaction,
// and returns a result struct or an error. Rendering text or JSON and mapping
// errors to exit codes belong to the command layer in cmd/nju-connect.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/soundadam/nju-connect/internal/backend/atrust"
	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/speedtest"
)

// Deps are the side effects a service may use. Tests build their own.
type Deps struct {
	// Paths resolves the state directory: NJU_CONNECT_CONFIG_DIR, or the
	// user configuration directory.
	Paths func() (config.Paths, error)
	// PasswordStore opens the long-lived VPN password shared by both
	// backends, at config.Paths.Credential.
	PasswordStore func(path string) (credential.Store, error)
	// ATrustSessionStore opens the saved aTrust client data, at
	// config.Paths.ATrustClientData.
	ATrustSessionStore func(path string) (credential.Store, error)
	// ATrustCore constructs the aTrust protocol core linked into this build.
	ATrustCore func() atrustbackend.Core
	// OAuthHelper locates the bundled aTrust OAuth helper, if any.
	OAuthHelper func() (string, bool)
	// EasyConnectSession builds the in-process EasyConnect runtime for an
	// authenticated session.
	EasyConnectSession func(nativeapp.SessionConfig) (NativeSession, error)
	// StartBackground hands an authenticated EasyConnect session to a
	// detached runtime process whose output goes to logPath, and returns its
	// process ID once it is connected.
	StartBackground func(sessionConfig nativeapp.SessionConfig, logPath string) (int, error)
	// Speedtest locates and runs the campus speed-test helper.
	Speedtest SpeedtestDeps
	// Stdin is where the command layer reads answers and piped secrets.
	Stdin *os.File
	// Interaction asks the user for input.
	Interaction Interaction
	// Interactive reports whether a person answers Interaction, which
	// enables guided flows such as offering setup or re-entering a rejected
	// password. It is false when answers are piped in.
	Interactive bool
	// Diagnostics receives helper-process output meant for the user. The
	// command layer points it at stderr so stdout stays machine-readable.
	Diagnostics io.Writer
}

// SpeedtestDeps are the side effects of the campus speed test.
type SpeedtestDeps struct {
	// Asset describes the downloadable helper for this platform.
	Asset func() speedtest.ComponentAsset
	// ExternalPath locates an installed external helper, or returns "".
	ExternalPath func() string
	// HTTPClient downloads the helper; nil uses the default client.
	HTTPClient func() *http.Client
	// Probe checks that the campus target is reachable over a route.
	Probe func(ctx context.Context, route speedtest.Route, socksListen string) error
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
