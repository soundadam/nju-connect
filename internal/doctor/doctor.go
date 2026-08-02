// Package doctor provides a read-only and secret-free development diagnostic.
package doctor

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/dial"
)

type Report struct {
	Ready          bool   `json:"ready"`
	Configuration  string `json:"configuration"`
	CredentialFile string `json:"credential_file"`
	UpstreamProxy  string `json:"upstream_proxy"`
}

func Build(paths config.Paths) Report {
	report := Report{
		Configuration:  "invalid",
		CredentialFile: "not_checked",
		UpstreamProxy:  "not_checked",
	}
	configured, err := config.Load(paths.Config)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Configuration = "missing"
		}
		return report
	}
	report.Configuration = "ready"

	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		report.CredentialFile = "invalid"
		return report
	}
	switch err := store.Inspect(); {
	case err == nil:
		report.CredentialFile = "ready"
	case errors.Is(err, os.ErrNotExist):
		report.CredentialFile = "missing"
	default:
		report.CredentialFile = "invalid"
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
		report.CredentialFile == "ready" &&
		(report.UpstreamProxy == "ready" || report.UpstreamProxy == "not_configured")
	return report
}
