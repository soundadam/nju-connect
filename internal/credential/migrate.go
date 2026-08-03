package credential

import (
	"errors"
	"fmt"
	"os"
)

// MigrateFile imports an existing owner-only credential file into destination
// only when destination is empty. The source is preserved so migration is
// recoverable; callers may remove it after separately confirming the new
// platform store.
func MigrateFile(destination Store, sourcePath string) (bool, error) {
	if destination == nil {
		return false, errors.New("destination credential store is required")
	}
	if err := destination.Inspect(); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect destination credential: %w", err)
	}

	source, err := NewFileStore(sourcePath, true)
	if err != nil {
		return false, err
	}
	secret, err := source.Get()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy credential: %w", err)
	}
	defer Clear(secret)
	if err := destination.Set(secret); err != nil {
		return false, fmt.Errorf("store migrated credential: %w", err)
	}
	if err := destination.Inspect(); err != nil {
		return false, fmt.Errorf("verify migrated credential: %w", err)
	}
	return true, nil
}
