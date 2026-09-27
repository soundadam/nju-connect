//go:build darwin || linux

package owneronly

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// Owned reports whether the current user owns the path info describes.
func Owned(_ string, info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(stat.Uid) == int64(os.Geteuid())
}

// Restricted returns an error when info's mode has permission bits outside
// perm.
func Restricted(_ string, info fs.FileInfo, perm fs.FileMode) error {
	if got := info.Mode().Perm(); got&^perm != 0 {
		return fmt.Errorf("mode %04o", got)
	}
	return nil
}

// Restrict sets file's mode to perm.
func Restrict(file *os.File, perm fs.FileMode) error {
	return file.Chmod(perm)
}

// MkdirAll creates path and any missing parents; a directory it creates has
// mode 0700.
func MkdirAll(path string) error {
	return os.MkdirAll(path, 0700)
}
