//go:build !darwin && !linux

package credential

import "io/fs"

func ownedByCurrentUser(fs.FileInfo) bool {
	return false
}
