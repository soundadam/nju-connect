package credential

import (
	"errors"
	"fmt"
	"os"
)

// Credential store backends selectable through config.toml's
// credential_store. The keyring is the default everywhere; the file backend
// exists for Linux hosts without a Secret Service, such as SSH sessions.
const (
	BackendKeyring = "keyring"
	BackendFile    = "file"
)

// Location names one saved secret.
type Location struct {
	// Backend is BackendKeyring (also when empty) or BackendFile.
	Backend string
	Service string
	Account string
	// File is the owner-only file the file backend uses. With the keyring
	// backend, a secret an earlier release left there is moved into the
	// keyring on first use and the file is removed.
	File string
}

// ValidateBackend accepts the credential_store values.
func ValidateBackend(name string) error {
	switch name {
	case "", BackendKeyring, BackendFile:
		return nil
	}
	return fmt.Errorf("credential store %q is not %q or %q", name, BackendKeyring, BackendFile)
}

// Open returns the store for location. Keyring stores also move secrets
// left behind by earlier releases: the pre-keyring macOS Keychain item and
// the owner-only file. The move happens once, the first time the secret is
// read, so a cleared secret is never resurrected.
func Open(location Location) (Clearable, error) {
	if err := ValidateBackend(location.Backend); err != nil {
		return nil, err
	}
	if location.Backend == BackendFile {
		return NewFileStore(location.File, true)
	}
	primary, err := NewKeyringStore(location.Service, location.Account)
	if err != nil {
		return nil, err
	}
	store := &migratingStore{primary: primary}
	if legacy := legacyKeychainStore(location); legacy != nil {
		store.legacy = append(store.legacy, legacy)
	}
	if location.File != "" {
		file, err := NewFileStore(location.File, true)
		if err != nil {
			return nil, err
		}
		store.legacy = append(store.legacy, file)
	}
	return store, nil
}

// migratingStore reads through to legacy stores until the primary store
// holds the secret, then moves it and removes the legacy copies.
type migratingStore struct {
	primary Clearable
	legacy  []Clearable
}

func (store *migratingStore) Inspect() error {
	err := store.primary.Inspect()
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, legacy := range store.legacy {
		if legacyErr := legacy.Inspect(); !errors.Is(legacyErr, os.ErrNotExist) {
			return legacyErr
		}
	}
	return err
}

func (store *migratingStore) Get() ([]byte, error) {
	secret, err := store.primary.Get()
	if !errors.Is(err, os.ErrNotExist) {
		return secret, err
	}
	for _, legacy := range store.legacy {
		migrated, migrateErr := Migrate(store.primary, legacy)
		if migrateErr != nil {
			return nil, migrateErr
		}
		if migrated {
			if err := store.clearLegacy(); err != nil {
				return nil, fmt.Errorf("remove legacy credential: %w", err)
			}
			return store.primary.Get()
		}
	}
	return nil, err
}

func (store *migratingStore) Set(secret []byte) error {
	if err := store.primary.Set(secret); err != nil {
		return err
	}
	// A stale legacy copy would come back after the next Clear.
	if err := store.clearLegacy(); err != nil {
		return fmt.Errorf("remove legacy credential: %w", err)
	}
	return nil
}

func (store *migratingStore) Clear() error {
	return errors.Join(store.primary.Clear(), store.clearLegacy())
}

func (store *migratingStore) clearLegacy() error {
	var errs []error
	for _, legacy := range store.legacy {
		// Inspecting first avoids touching, and on macOS prompting for,
		// legacy items that are already gone.
		if err := legacy.Inspect(); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := legacy.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
