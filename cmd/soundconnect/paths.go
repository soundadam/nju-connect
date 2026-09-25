package main

import (
	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

var (
	resolveDefaultPaths      = config.DefaultPaths
	newSystemCredentialStore = openCredentialStore
	newATrustClientDataStore = openCredentialStore
)

func openCredentialStore(location credential.Location) (credential.Store, error) {
	return credential.Open(location)
}

func commandPaths() (config.Paths, error) {
	return resolveDefaultPaths()
}

// commandPasswordStore opens the shared password for a loaded configuration.
func commandPasswordStore(paths config.Paths, configured config.Config) (credential.Store, error) {
	return newSystemCredentialStore(app.PasswordLocation(paths, configured.CredentialStore))
}
