package credential

import (
	"errors"
	"fmt"
	"os"
)

// Migrate copies source into destination only when destination is empty.
// The source is preserved; callers decide whether to clear it.
func Migrate(destination, source Store) (bool, error) {
	if destination == nil || source == nil {
		return false, errors.New("destination and source credential stores are required")
	}
	if err := destination.Inspect(); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect destination credential: %w", err)
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
