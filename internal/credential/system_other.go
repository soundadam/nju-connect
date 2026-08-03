//go:build !darwin

package credential

// NewSystemStore retains the explicit owner-only file backend on platforms
// where a native credential adapter has not been implemented.
func NewSystemStore(legacyPath string) (Store, error) {
	return NewFileStore(legacyPath, true)
}
