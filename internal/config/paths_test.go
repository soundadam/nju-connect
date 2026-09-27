package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/soundadam/nju-connect/internal/credential"
)

func TestLegacyPathsStayInsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	paths, err := LegacyPaths(worktree)
	if err != nil {
		t.Fatalf("LegacyPaths() error = %v", err)
	}

	wantRoot := filepath.Join(worktree, ".config")
	if paths.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", paths.Root, wantRoot)
	}
	if paths.Config != filepath.Join(wantRoot, "config.toml") {
		t.Fatalf("Config = %q", paths.Config)
	}
	if paths.Credential != filepath.Join(wantRoot, "credential") {
		t.Fatalf("Credential = %q", paths.Credential)
	}
	if paths.ATrustClientData != filepath.Join(wantRoot, "atrust-client-data") {
		t.Fatalf("ATrustClientData = %q", paths.ATrustClientData)
	}
}

func TestApplicationPathsUseStableFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), applicationDirectory)
	paths := pathsAt(root)

	if paths.Root != root {
		t.Fatalf("Root = %q, want %q", paths.Root, root)
	}
	if paths.Config != filepath.Join(root, "config.toml") {
		t.Fatalf("Config = %q", paths.Config)
	}
	if paths.Credential != filepath.Join(root, "credential") {
		t.Fatalf("Credential = %q", paths.Credential)
	}
	if paths.ATrustClientData != filepath.Join(root, "atrust-client-data") {
		t.Fatalf("ATrustClientData = %q", paths.ATrustClientData)
	}
}

// isolateUserConfigDir points os.UserConfigDir at a temporary directory, so
// DefaultPaths never moves the real state directory, and returns it.
func isolateUserConfigDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(configDirectoryEnv, "")
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return configDir
}

func TestDefaultPathsUseUserConfigDirectory(t *testing.T) {
	isolateUserConfigDir(t)
	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(paths.Root) != applicationDirectory {
		t.Fatalf("Root = %q, want suffix %q", paths.Root, applicationDirectory)
	}
	if paths.KeyringService != credential.DefaultKeyringService {
		t.Fatalf("KeyringService = %q", paths.KeyringService)
	}
}

func TestDefaultPathsUseExplicitConfigDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "atrust-test")
	t.Setenv(configDirectoryEnv, root)

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != root {
		t.Fatalf("Root = %q, want %q", paths.Root, root)
	}
	// An isolated directory never shares the real keyring items.
	if paths.KeyringService != credential.KeyringService(root, true) || paths.KeyringService == credential.DefaultKeyringService {
		t.Fatalf("KeyringService = %q", paths.KeyringService)
	}
}

func TestDefaultPathsRejectRelativeConfigDirectory(t *testing.T) {
	t.Setenv(configDirectoryEnv, filepath.Join("relative", "atrust-test"))

	_, err := DefaultPaths()
	if err == nil {
		t.Fatal("DefaultPaths() unexpectedly accepted a relative override")
	}
}

func TestDefaultPathsAdoptTheSoundconnectDirectoryOnce(t *testing.T) {
	configDir := isolateUserConfigDir(t)
	legacy := filepath.Join(configDir, legacyApplicationDirectory)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.toml"), []byte("backend = 'atrust'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(paths.Config); err != nil || string(content) != "backend = 'atrust'\n" {
		t.Fatalf("config after move = %q, %v", content, err)
	}
	if _, err := os.Lstat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("earlier directory still exists: %v", err)
	}

	// A later soundconnect directory is left alone once nju-connect exists.
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultPaths(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(legacy); err != nil {
		t.Fatalf("second run touched the earlier directory: %v", err)
	}
}
