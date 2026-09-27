package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fileLocation(t *testing.T, account string) Location {
	t.Helper()
	root := t.TempDir()
	return Location{Service: KeyringService(root, true), Account: account, File: filepath.Join(root, "nju-connect", account)}
}

func writeFileSecret(t *testing.T, path, secret string) *FileStore {
	t.Helper()
	store, err := NewFileStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte(secret)); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestOpenSelectsTheConfiguredBackend(t *testing.T) {
	location := fileLocation(t, PasswordAccount)
	location.Backend = BackendFile
	store, err := Open(location)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.(*FileStore); !ok {
		t.Fatalf("file backend store = %T", store)
	}
	location.Backend = "vault"
	if _, err := Open(location); err == nil {
		t.Fatal("unknown backend accepted")
	}
}

func TestOpenMovesALegacyFileIntoTheKeyringOnce(t *testing.T) {
	location := fileLocation(t, PasswordAccount)
	legacy := writeFileSecret(t, location.File, "legacy-password")
	store, err := Open(location)
	if err != nil {
		t.Fatal(err)
	}
	// Inspect reports the legacy secret without moving it.
	if err := store.Inspect(); err != nil {
		t.Fatalf("Inspect() = %v", err)
	}
	if err := legacy.Inspect(); err != nil {
		t.Fatalf("Inspect() moved the secret: %v", err)
	}
	if secret, err := store.Get(); err != nil || string(secret) != "legacy-password" {
		t.Fatalf("Get() = %q, %v", secret, err)
	}
	if _, err := os.Lstat(location.File); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy file remains: %v", err)
	}
	keyringOnly, _ := NewKeyringStore(location.Service, location.Account)
	if secret, err := keyringOnly.Get(); err != nil || string(secret) != "legacy-password" {
		t.Fatalf("keyring secret = %q, %v", secret, err)
	}

	// Clearing is final: nothing comes back on the next read.
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get() after Clear = %v", err)
	}
}

// Releases named soundconnect used another keyring service. Their items,
// chunked ones included, move under the new service on the first read.
func TestOpenMovesSoundconnectKeyringItemsOnce(t *testing.T) {
	location := fileLocation(t, ATrustSessionAccount)
	service, ok := legacyKeyringService(location.Service)
	if !ok || service == location.Service {
		t.Fatalf("legacyKeyringService(%q) = %q, %t", location.Service, service, ok)
	}
	renamed, err := NewKeyringStore(service, location.Account)
	if err != nil {
		t.Fatal(err)
	}
	large := string(make([]byte, 3*maximumChunkRaw))
	if err := renamed.Set([]byte(large)); err != nil {
		t.Fatal(err)
	}
	store, err := Open(location)
	if err != nil {
		t.Fatal(err)
	}
	if secret, err := store.Get(); err != nil || string(secret) != large {
		t.Fatalf("Get() = %d bytes, %v", len(secret), err)
	}
	if err := renamed.Inspect(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("soundconnect item remains: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get() after Clear = %v", err)
	}
}

func TestLegacyKeyringServiceCoversDefaultAndIsolatedServices(t *testing.T) {
	if got, ok := legacyKeyringService(DefaultKeyringService); !ok || got != LegacyKeyringService {
		t.Fatalf("default = %q, %t", got, ok)
	}
	isolated := KeyringService("/tmp/profile", true)
	if got, ok := legacyKeyringService(isolated); !ok || got != LegacyKeyringService+isolated[len(DefaultKeyringService):] {
		t.Fatalf("isolated = %q, %t", got, ok)
	}
	if _, ok := legacyKeyringService("com.example.other"); ok {
		t.Fatal("unrelated service mapped")
	}
}

func TestOpenPrefersTheKeyringAndSetRemovesStaleLegacyCopies(t *testing.T) {
	location := fileLocation(t, ATrustSessionAccount)
	store, err := Open(location)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte("current")); err != nil {
		t.Fatal(err)
	}
	writeFileSecret(t, location.File, "stale")
	if secret, err := store.Get(); err != nil || string(secret) != "current" {
		t.Fatalf("Get() = %q, %v", secret, err)
	}
	if err := store.Set([]byte("newer")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(location.File); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale legacy file remains: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Inspect(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Inspect() after Clear = %v", err)
	}
}

func TestOpenReportsAnUnsafeLegacyFile(t *testing.T) {
	location := fileLocation(t, PasswordAccount)
	writeFileSecret(t, location.File, "legacy-password")
	if err := os.Chmod(location.File, 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(location)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Inspect(); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Inspect() = %v", err)
	}
	if _, err := store.Get(); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Get() = %v", err)
	}
}

func TestMigrateCopiesOnlyIntoAnEmptyDestination(t *testing.T) {
	source := &memoryStore{secret: []byte("legacy")}
	destination := &memoryStore{}
	if migrated, err := Migrate(destination, source); err != nil || !migrated || string(destination.secret) != "legacy" {
		t.Fatalf("Migrate() = %t, %v", migrated, err)
	}
	source.secret = []byte("other")
	if migrated, err := Migrate(destination, source); err != nil || migrated || string(destination.secret) != "legacy" {
		t.Fatalf("second Migrate() = %t, %v", migrated, err)
	}
	if _, err := Migrate(nil, source); err == nil {
		t.Fatal("nil destination accepted")
	}
}
