// Package doctor provides a read-only and secret-free development diagnostic.
package doctor

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/dial"
)

type Report struct {
	Ready           bool   `json:"ready"`
	Configuration   string `json:"configuration"`
	CredentialStore string `json:"credential_store"`
	UpstreamProxy   string `json:"upstream_proxy"`
	// NextStep is the command that moves the user forward.
	NextStep string `json:"next_step"`
}

// Next steps reported by Build.
const (
	NextSetup          = "nju-connect setup"
	NextSetPassword    = "nju-connect account set-password"
	NextConfigureProxy = "nju-connect configure --upstream-proxy"
	NextConnect        = "nju-connect connect"
)

func Build(paths config.Paths, store credential.Store) Report {
	report := build(paths, store)
	switch {
	case report.Configuration != "ready" || report.CredentialStore == "invalid":
		report.NextStep = NextSetup
	case report.CredentialStore == "missing":
		report.NextStep = NextSetPassword
	case report.UpstreamProxy == "unavailable":
		report.NextStep = NextConfigureProxy
	default:
		report.NextStep = NextConnect
	}
	return report
}

func build(paths config.Paths, store credential.Store) Report {
	report := Report{
		Configuration:   "invalid",
		CredentialStore: "not_checked",
		UpstreamProxy:   "not_checked",
	}
	configured, err := config.Load(paths.Config)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Configuration = "missing"
		}
		return report
	}
	report.Configuration = "ready"

	if configured.BackendName() == backend.ATrust && configured.AuthType != "auth/psw" {
		// aTrust OAuth keeps no long-lived password.
		report.CredentialStore = "not_required"
	} else if store == nil {
		report.CredentialStore = "invalid"
		return report
	} else {
		switch err := store.Inspect(); {
		case err == nil:
			report.CredentialStore = "ready"
		case errors.Is(err, os.ErrNotExist):
			report.CredentialStore = "missing"
		default:
			report.CredentialStore = "invalid"
		}
	}

	if configured.UpstreamProxy == "" {
		report.UpstreamProxy = "not_configured"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := dial.ProbeUpstream(ctx, configured.UpstreamProxy)
		cancel()
		if err != nil {
			report.UpstreamProxy = "unavailable"
		} else {
			report.UpstreamProxy = "ready"
		}
	}
	report.Ready = report.Configuration == "ready" &&
		(report.CredentialStore == "ready" || report.CredentialStore == "not_required") &&
		(report.UpstreamProxy == "ready" || report.UpstreamProxy == "not_configured")
	return report
}
