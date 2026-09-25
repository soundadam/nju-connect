package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/soundadam/soundconnect/internal/credential"
)

const (
	applicationDirectory = "soundconnect"
	legacyLocalDirectory = ".config"
	configDirectoryEnv   = "SOUNDCONNECT_CONFIG_DIR"
)

// Paths names the user configuration and credential files owned by
// soundconnect.
type Paths struct {
	Root             string
	Config           string
	Credential       string
	ATrustClientData string
	// KeyringService names this state directory's system keyring items.
	KeyringService string
}

// DefaultPaths resolves the operating-system user configuration directory.
// On Linux this follows XDG_CONFIG_HOME, normally ~/.config; on macOS it
// follows os.UserConfigDir's Application Support location.
func DefaultPaths() (Paths, error) {
	if configured := os.Getenv(configDirectoryEnv); configured != "" {
		if !filepath.IsAbs(configured) {
			return Paths{}, fmt.Errorf("%s must be an absolute path", configDirectoryEnv)
		}
		paths := pathsAt(filepath.Clean(configured))
		paths.KeyringService = credential.KeyringService(paths.Root, true)
		return paths, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	return pathsAt(filepath.Join(configDir, applicationDirectory)), nil
}

// LegacyPaths resolves the pre-release worktree-local layout. It exists only
// as an input to migration and must not be used as an active runtime root.
func LegacyPaths(worktree string) (Paths, error) {
	if worktree == "" {
		return Paths{}, errors.New("legacy root path is required")
	}

	root, err := filepath.Abs(filepath.Join(worktree, legacyLocalDirectory))
	if err != nil {
		return Paths{}, err
	}
	return pathsAt(root), nil
}

func pathsAt(root string) Paths {
	return Paths{
		Root:             root,
		Config:           filepath.Join(root, "config.toml"),
		Credential:       filepath.Join(root, "credential"),
		ATrustClientData: filepath.Join(root, "atrust-client-data"),
		KeyringService:   credential.DefaultKeyringService,
	}
}
