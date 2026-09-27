package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	applicationDirectory = "nju-connect"
	configDirectoryEnv   = "NJU_CONNECT_CONFIG_DIR"
)

// Paths names the user configuration and secret files owned by nju-connect.
// Credential holds the long-lived VPN password; ATrustClientData holds the
// saved aTrust session. Both are owner-only files under Root.
type Paths struct {
	Root             string
	Config           string
	Credential       string
	ATrustClientData string
}

// DefaultPaths resolves the operating-system user configuration directory.
// On Linux this follows XDG_CONFIG_HOME, normally ~/.config; on macOS it
// follows os.UserConfigDir's Application Support location.
func DefaultPaths() (Paths, error) {
	if configured := os.Getenv(configDirectoryEnv); configured != "" {
		if !filepath.IsAbs(configured) {
			return Paths{}, fmt.Errorf("%s must be an absolute path", configDirectoryEnv)
		}
		return pathsAt(filepath.Clean(configured)), nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	return pathsAt(filepath.Join(configDir, applicationDirectory)), nil
}

func pathsAt(root string) Paths {
	return Paths{
		Root:             root,
		Config:           filepath.Join(root, "config.toml"),
		Credential:       filepath.Join(root, "credential"),
		ATrustClientData: filepath.Join(root, "atrust-client-data"),
	}
}
