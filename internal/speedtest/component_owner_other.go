//go:build !linux && !darwin

package speedtest

import (
	"errors"
	"os"
)

func validateComponentOwner(os.FileInfo) error {
	return errors.New("campus speed-test components are supported only on macOS")
}

func syncDirectory(string) error { return nil }
