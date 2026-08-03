package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyConfigCopiesValidatedConfigAndPreservesSource(t *testing.T) {
	legacyRoot := t.TempDir()
	legacy, err := LegacyPaths(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: DefaultSOCKSListen}
	if err := Replace(legacy.Config, want); err != nil {
		t.Fatal(err)
	}
	destination := pathsAt(filepath.Join(t.TempDir(), applicationDirectory))

	gotLegacy, migrated, err := MigrateLegacyConfig(legacyRoot, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated || gotLegacy != legacy {
		t.Fatalf("migrated = %t, legacy = %#v", migrated, gotLegacy)
	}
	got, err := Load(destination.Config)
	if err != nil || got != want {
		t.Fatalf("migrated config = %#v, err = %v", got, err)
	}
	if _, err := os.Stat(legacy.Config); err != nil {
		t.Fatalf("legacy config was not preserved: %v", err)
	}
}

func TestMigrateLegacyConfigDoesNotOverwriteDestination(t *testing.T) {
	legacyRoot := t.TempDir()
	legacy, err := LegacyPaths(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := Replace(legacy.Config, Config{Server: "old.example.edu", Username: "old", SOCKSListen: DefaultSOCKSListen}); err != nil {
		t.Fatal(err)
	}
	destination := pathsAt(filepath.Join(t.TempDir(), applicationDirectory))
	want := Config{Server: "new.example.edu", Username: "new", SOCKSListen: DefaultSOCKSListen}
	if err := Replace(destination.Config, want); err != nil {
		t.Fatal(err)
	}

	_, migrated, err := MigrateLegacyConfig(legacyRoot, destination)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("existing destination was overwritten")
	}
	got, err := Load(destination.Config)
	if err != nil || got != want {
		t.Fatalf("destination config = %#v, err = %v", got, err)
	}
}
