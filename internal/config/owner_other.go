//go:build !darwin && !linux

package config

import "io/fs"

func ownedByCurrentUser(fs.FileInfo) bool {
	return false
}
