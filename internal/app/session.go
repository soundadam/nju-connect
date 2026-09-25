package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
)

const atrustLogoutTimeout = 15 * time.Second

// LogoutResult reports what Logout cleared.
type LogoutResult struct {
	OAuthProfileCleared bool
}

// Logout forgets only aTrust authentication state: the saved client data
// and the OAuth helper's browser profile. It deliberately keeps the shared
// password and the non-secret configuration.
func Logout(ctx context.Context, deps Deps) (LogoutResult, error) {
	paths, err := deps.Paths()
	if err != nil {
		return LogoutResult{}, fmt.Errorf("resolve local state: %w", err)
	}
	store, err := deps.ATrustSessionStore(paths.ATrustClientData)
	if err != nil {
		return LogoutResult{}, fmt.Errorf("prepare aTrust session store: %w", err)
	}
	clearable, ok := store.(credential.Clearable)
	if !ok {
		return LogoutResult{}, errors.New("aTrust session store does not support clearing")
	}
	if err := clearable.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LogoutResult{}, fmt.Errorf("clear aTrust session: %w", err)
	}

	helperPath, available := deps.OAuthHelper()
	if !available {
		if runtime.GOOS == "darwin" {
			return LogoutResult{}, errors.New("clear OAuth profile: bundled aTrust OAuth helper is unavailable")
		}
		return LogoutResult{}, nil
	}
	clearContext, cancel := context.WithTimeout(ctx, atrustLogoutTimeout)
	defer cancel()
	command := exec.CommandContext(clearContext, helperPath, "--clear-data")
	command.Stdout = io.Discard
	command.Stderr = deps.diagnostics()
	if err := command.Run(); err != nil {
		if clearContext.Err() != nil {
			return LogoutResult{}, errors.New("clear OAuth profile: timed out")
		}
		return LogoutResult{}, fmt.Errorf("clear OAuth profile: %w", err)
	}
	return LogoutResult{OAuthProfileCleared: true}, nil
}

// Status reads the snapshot of the runtime that belongs to this state
// directory, or runtimecontrol.Stopped when none is running.
func Status(deps Deps) (runtimecontrol.Snapshot, error) {
	paths, err := deps.Paths()
	if err != nil {
		return runtimecontrol.Snapshot{}, fmt.Errorf("resolve local state: %w", err)
	}
	snapshot, err := runtimecontrol.Current(paths.Root)
	if err != nil {
		return runtimecontrol.Snapshot{}, fmt.Errorf("read runtime status: %w", err)
	}
	return snapshot, nil
}

// Disconnect asks the running runtime to stop. It reports false, and no
// error, when nothing is running.
func Disconnect(deps Deps) (stopping bool, err error) {
	paths, err := deps.Paths()
	if err != nil {
		return false, fmt.Errorf("resolve local state: %w", err)
	}
	err = runtimecontrol.RequestDisconnect(runtimecontrol.Path(paths.Root))
	if errors.Is(err, runtimecontrol.ErrNotRunning) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("disconnect runtime: %w", err)
	}
	return true, nil
}
