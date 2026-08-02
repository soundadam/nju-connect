package config

import (
	"errors"
	"os"
	"path/filepath"
)

const localDirectory = ".config"

// Paths names the development-local files rooted in a soundconnect worktree.
type Paths struct {
	Root       string
	Config     string
	Credential string
}

// DefaultPaths resolves development state from the current working directory.
func DefaultPaths() (Paths, error) {
	worktree, err := os.Getwd()
	if err != nil {
		return Paths{}, err
	}
	return LocalPaths(worktree)
}

// LocalPaths resolves development state beneath worktree without consulting
// the user's operating-system configuration directory.
func LocalPaths(worktree string) (Paths, error) {
	if worktree == "" {
		return Paths{}, errors.New("worktree path is required")
	}

	root, err := filepath.Abs(filepath.Join(worktree, localDirectory))
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}, nil
}
