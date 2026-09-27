// Package owneronly checks and creates files and directories that only the
// current user can access. On darwin and linux that means the current euid
// owns the path and its mode has no bits outside a given permission mask. On
// Windows the owner SID must be the current token user, and the DACL may grant
// access only to that user, SYSTEM, and Administrators; newly created paths
// get a protected DACL naming the current user alone.
package owneronly

import "errors"

// ErrUnsupported reports a platform with no ownership check. Every check
// fails closed there.
var ErrUnsupported = errors.New("owner-only file checks are not implemented on this platform")
