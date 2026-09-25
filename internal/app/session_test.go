//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeHelper(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLogoutClearsSessionAndOAuthProfileButKeepsPassword(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setSecret(env.paths.Credential, "synthetic-password")
	env.setSecret(env.paths.ATrustClientData, "client-data")
	marker := filepath.Join(t.TempDir(), "cleared")
	helper := writeHelper(t, `[ "$1" = "--clear-data" ] && touch "`+marker+`"`)
	env.deps.OAuthHelper = func() (string, bool) { return helper, true }

	result, err := Logout(context.Background(), env.deps)
	if err != nil || !result.OAuthProfileCleared {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := env.secret(env.paths.ATrustClientData); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aTrust session remains: %v", err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("helper was not asked to clear its profile: %v", err)
	}
}

func TestLogoutReportsHelperFailure(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	var diagnostics bytes.Buffer
	env.deps.Diagnostics = &diagnostics
	helper := writeHelper(t, "echo 'profile locked' >&2; exit 3\n")
	env.deps.OAuthHelper = func() (string, bool) { return helper, true }

	_, err := Logout(context.Background(), env.deps)
	if err == nil || !strings.HasPrefix(err.Error(), "clear OAuth profile: exit status 3") {
		t.Fatalf("err = %v", err)
	}
	if diagnostics.String() != "profile locked\n" {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
}

func TestLogoutWithoutHelper(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	result, err := Logout(context.Background(), env.deps)
	if runtime.GOOS == "darwin" {
		// The macOS build always bundles the helper, so its absence is an error.
		if err == nil || err.Error() != "clear OAuth profile: bundled aTrust OAuth helper is unavailable" {
			t.Fatalf("err = %v", err)
		}
		return
	}
	if err != nil || result.OAuthProfileCleared {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStatusAndDisconnect(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	snapshot, err := Status(env.deps)
	if err != nil || snapshot.Running || snapshot.State != "stopped" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if stopping, err := Disconnect(env.deps); stopping || err != nil {
		t.Fatalf("Disconnect() without runtime = %t, %v", stopping, err)
	}

	server := env.serveRuntime()
	defer server.Close()
	stopped := make(chan struct{})
	server.SetStop(func() { close(stopped) })
	snapshot, err = Status(env.deps)
	if err != nil || !snapshot.Running || snapshot.State != "connected" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if stopping, err := Disconnect(env.deps); !stopping || err != nil {
		t.Fatalf("Disconnect() = %t, %v", stopping, err)
	}
	<-stopped
}
