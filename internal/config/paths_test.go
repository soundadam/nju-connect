package config

import (
	"path/filepath"
	"testing"
)

func TestLocalPathsStayInsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	paths, err := LocalPaths(worktree)
	if err != nil {
		t.Fatalf("LocalPaths() error = %v", err)
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
}
