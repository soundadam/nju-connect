package main

import (
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

var (
	resolveDefaultPaths      = config.DefaultPaths
	newSystemCredentialStore = credential.NewSystemStore
)

func commandPaths() (config.Paths, error) {
	return resolveDefaultPaths()
}

func commandCredentialStore(paths config.Paths) (credential.Store, bool, error) {
	store, err := newSystemCredentialStore(paths.Credential)
	if err != nil {
		return nil, false, err
	}
	migrated, err := credential.MigrateFile(store, paths.Credential)
	if err != nil {
		return nil, false, err
	}
	return store, migrated, nil
}
