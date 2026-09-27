package main

import (
	"io"
	"net/http"
	"os"

	"github.com/soundadam/nju-connect/internal/backend/atrust"

	"github.com/soundadam/nju-connect/internal/app"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/speedtest"
	"github.com/soundadam/nju-connect/internal/tui"
)

// productionDeps are the real side effects: the user's state directory and
// its secret files, the linked aTrust core, and this process's stdin.
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

func openCredentialStore(path string) (credential.Store, error) {
	return credential.NewFileStore(path)
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
