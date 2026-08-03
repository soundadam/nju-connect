package config

import (
	"errors"
	"fmt"
	"os"
)

// MigrateLegacyConfig copies a validated pre-release worktree configuration
// into the OS user configuration directory without overwriting either side.
// The source is preserved for an explicit, recoverable migration.
func MigrateLegacyConfig(legacyRoot string, destination Paths) (Paths, bool, error) {
	legacy, err := LegacyPaths(legacyRoot)
	if err != nil {
		return Paths{}, false, err
	}
	if _, err := os.Lstat(destination.Config); err == nil {
		return legacy, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return legacy, false, fmt.Errorf("inspect destination config: %w", err)
	}

	configured, err := Load(legacy.Config)
	if err != nil {
		return legacy, false, fmt.Errorf("load legacy config: %w", err)
	}
	if err := Replace(destination.Config, configured); err != nil {
		return legacy, false, fmt.Errorf("store migrated config: %w", err)
	}
	return legacy, true, nil
}
