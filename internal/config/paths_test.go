package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
}

func TestDefaultPathsRejectRelativeConfigDirectory(t *testing.T) {
	t.Setenv(configDirectoryEnv, filepath.Join("relative", "atrust-test"))

	_, err := DefaultPaths()
	if err == nil {
		t.Fatal("DefaultPaths() unexpectedly accepted a relative override")
	}
}
