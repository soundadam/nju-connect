//go:build !darwin

package credential

// NewSystemStore retains the explicit owner-only file backend on platforms
// where a native credential adapter has not been implemented.
func NewSystemStore(legacyPath string) (Store, error) {
	return NewFileStore(legacyPath, true)
}

// NewATrustClientDataStore retains an owner-only file fallback on platforms
// without a native Keychain adapter. macOS uses the Keychain implementation.
func NewATrustClientDataStore(path string) (Store, error) {
	return NewFileStore(path, true)
}
