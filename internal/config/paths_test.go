package config

import (
	"path/filepath"
	"testing"

	"github.com/soundadam/soundconnect/internal/credential"
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

func TestDefaultPathsUseUserConfigDirectory(t *testing.T) {
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
