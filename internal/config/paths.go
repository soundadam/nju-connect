package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/soundadam/nju-connect/internal/credential"
)

const (
	applicationDirectory = "nju-connect"
	// legacyApplicationDirectory is the state directory of releases named
	// soundconnect. DefaultPaths moves it into place once.
	legacyApplicationDirectory = "soundconnect"
	legacyLocalDirectory       = ".config"
	configDirectoryEnv         = "NJU_CONNECT_CONFIG_DIR"
)

// Paths names the user configuration and credential files owned by
// nju-connect.
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
	root := filepath.Join(configDir, applicationDirectory)
	if err := adoptLegacyDirectory(filepath.Join(configDir, legacyApplicationDirectory), root); err != nil {
		return Paths{}, err
	}
	return pathsAt(root), nil
}

// adoptLegacyDirectory moves the state directory an earlier release left at
// legacy to root, but only while root does not exist, so it runs once and
// never merges two directories.
func adoptLegacyDirectory(legacy, root string) error {
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	info, err := os.Lstat(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect earlier state directory: %w", err)
	}
	if !info.IsDir() {
		return nil
	}
	if err := os.Rename(legacy, root); err != nil {
		return fmt.Errorf("move earlier state directory %s to %s: %w", legacy, root, err)
	}
	return nil
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
