package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type memoryStore struct {
	secret []byte
	err    error
}

func (store *memoryStore) Inspect() error {
	if store.err != nil {
		return store.err
	}
	if store.secret == nil {
		return os.ErrNotExist
	}
	return nil
}

func (store *memoryStore) Get() ([]byte, error) {
	if err := store.Inspect(); err != nil {
		return nil, err
	}
	return append([]byte(nil), store.secret...), nil
}

func (store *memoryStore) Set(secret []byte) error {
	if store.err != nil {
		return store.err
	}
	store.secret = append(store.secret[:0], secret...)
	return nil
}

func TestMigrateFileImportsMissingDestinationAndPreservesSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nju-connect", "credential")
	source, err := NewFileStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Set([]byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}

	destination := &memoryStore{}
	migrated, err := MigrateFile(destination, path)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || string(destination.secret) != "synthetic-password" {
		t.Fatalf("migrated = %t, secret changed", migrated)
	}
	if err := source.Inspect(); err != nil {
		t.Fatalf("source was not preserved: %v", err)
	}
}

func TestMigrateFileDoesNotOverwriteExistingDestination(t *testing.T) {
	destination := &memoryStore{secret: []byte("current")}
	migrated, err := MigrateFile(destination, filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if migrated || string(destination.secret) != "current" {
		t.Fatal("existing destination was overwritten")
	}
}

func TestMigrateFileFailsClosedWhenDestinationCannotBeInspected(t *testing.T) {
	want := errors.New("locked")
	_, err := MigrateFile(&memoryStore{err: want}, filepath.Join(t.TempDir(), "credential"))
	if !errors.Is(err, want) {
		t.Fatalf("MigrateFile() error = %v", err)
	}
}
