package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/tui"
)

// errNoPassword tells the user how to save the missing password.
var errNoPassword = errors.New(`no saved VPN password; run "soundconnect account set-password"`)

// loadProfile loads the configuration for a command that needs one. On
// first run a terminal user is offered the guided setup; everyone else is
// told to run setup. offerSetup is false when standard input carries data.
func loadProfile(ctx context.Context, paths config.Paths, offerSetup bool, stderr io.Writer) (config.Config, error) {
	configured, err := config.Load(paths.Config)
	if err == nil {
		return configured, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, fmt.Errorf("load configuration: %w", err)
	}
	if offerSetup {
		interaction, interactive := tui.ForCommand(app.LineOptions{Input: os.Stdin}, stderr)
		if interactive {
			runSetup, err := interaction.Confirm(ctx, "No configuration yet. Run setup now?", true)
			if err != nil {
				return config.Config{}, err
			}
			if runSetup {
				result, err := app.Setup(ctx, commandDeps(interaction, stderr), app.SetupRequest{Guided: true})
				if err != nil {
					return config.Config{}, err
				}
				return result.Config, nil
			}
		}
	}
	return config.Config{}, errors.New(`load configuration: no configuration yet; run "soundconnect setup" first`)
}

// readSavedPassword reads the shared password, explaining how to save one
// when there is none.
func readSavedPassword(paths config.Paths, configured config.Config) ([]byte, error) {
	store, err := commandPasswordStore(paths, configured)
	if err != nil {
		return nil, fmt.Errorf("open credential: %w", err)
	}
	password, err := store.Get()
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, credential.ErrEmptyCredential) {
		return nil, errNoPassword
	}
	if err != nil {
		return nil, fmt.Errorf("read credential: %w", err)
	}
	return password, nil
}
