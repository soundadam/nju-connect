package app

import (
	"fmt"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/doctor"
)

// BackendCatalog is the `backends --json` contract shared with the macOS app.
type BackendCatalog struct {
	SchemaVersion int                  `json:"schema_version"`
	SOCKSListen   string               `json:"socks_listen"`
	Backends      []backend.Descriptor `json:"backends"`
}

// Backends describes the protocol backends linked into this build.
func Backends() BackendCatalog {
	return BackendCatalog{
		SchemaVersion: 1,
		SOCKSListen:   config.DefaultSOCKSListen,
		Backends:      backend.Catalog(),
	}
}

// Doctor inspects the local configuration and credential store without
// reading any secret.
func Doctor(deps Deps) (doctor.Report, error) {
	paths, err := deps.Paths()
	if err != nil {
		return doctor.Report{}, fmt.Errorf("resolve local state: %w", err)
	}
	store, err := deps.PasswordStore(PasswordLocation(paths, savedCredentialBackend(paths)))
	if err != nil {
		return doctor.Report{}, fmt.Errorf("prepare credential store: %w", err)
	}
	return doctor.Build(paths, store), nil
}
