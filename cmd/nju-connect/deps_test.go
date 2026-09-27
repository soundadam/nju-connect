package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/soundadam/nju-connect/internal/app"
	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/speedtest"
)

// testDeps runs commands against paths with owner-only file secrets, an
// empty non-terminal stdin, and no runtime, helper, or network. Tests
// replace the fields they exercise.
func testDeps(t *testing.T, paths config.Paths) app.Deps {
	t.Helper()
	deps := productionDeps()
	deps.Paths = func() (config.Paths, error) { return paths, nil }
	deps.EasyConnectSession = func(nativeapp.SessionConfig) (app.NativeSession, error) {
		return nil, errors.New("the test has no EasyConnect runtime")
	}
	deps.StartBackground = func(nativeapp.SessionConfig, string) (int, error) {
		return 0, errors.New("the test has no background runtime")
	}
	deps.Speedtest.ExternalPath = func() string { return "" }
	deps.Speedtest.Probe = func(context.Context, speedtest.Route, string) error {
		return errors.New("the test has no network")
	}
	deps.Stdin = emptyStdin(t)
	return deps
}

// emptyStdin is a non-terminal standard input with nothing to read.
func emptyStdin(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

// runConnectContext runs connect under ctx and returns its exit status.
func runConnectContext(ctx context.Context, deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	return runContext(ctx, deps, append([]string{"connect"}, arguments...), stdout, stderr)
}

// isolatedDeps is testDeps for a fresh, empty state directory.
func isolatedDeps(t *testing.T) app.Deps {
	t.Helper()
	t.Setenv("NJU_CONNECT_CONFIG_DIR", filepath.Join(t.TempDir(), "nju-connect"))
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	return testDeps(t, paths)
}

// stdinFile is a non-terminal standard input holding content.
func stdinFile(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

// answeredBy makes deps behave as if a person at a terminal typed script,
// one answer per line. Secrets are read as plain lines too.
func answeredBy(t *testing.T, deps app.Deps, script string) app.Deps {
	t.Helper()
	deps.Stdin = stdinFile(t, script)
	deps.Interaction = app.NewLineInteraction(app.LineOptions{Input: deps.Stdin, Output: io.Discard, PasswordFromStdin: true})
	deps.Interactive = true
	return deps
}
