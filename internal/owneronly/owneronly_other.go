//go:build !darwin && !linux && !windows

package owneronly

import (
	"io/fs"
	"os"
)

func Owned(string, fs.FileInfo) bool { return false }

func Restricted(string, fs.FileInfo, fs.FileMode) error { return ErrUnsupported }

func Restrict(*os.File, fs.FileMode) error { return ErrUnsupported }

func MkdirAll(string) error { return ErrUnsupported }
