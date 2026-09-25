//go:build darwin && !cgo

package credential

import "errors"

// NewSystemStore fails closed in compile-only builds that omit the Security
// framework bridge. macOS release artifacts must be built with cgo enabled.
func NewSystemStore(string) (Store, error) {
	return nil, errors.New("macOS Keychain support requires a cgo-enabled release build")
}

// NewATrustClientDataStore fails closed in compile-only builds that omit the
// Security framework bridge. macOS release artifacts must be built with cgo.
func NewATrustClientDataStore(string) (Store, error) {
	return nil, errors.New("macOS Keychain support requires a cgo-enabled release build")
}
