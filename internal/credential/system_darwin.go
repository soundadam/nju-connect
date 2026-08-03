//go:build darwin && cgo

package credential

const (
	keychainService = "com.soundadam.soundconnect"
	keychainAccount = "vpn-password"
)

// NewSystemStore uses the current user's login Keychain on macOS. The legacy
// path is intentionally ignored here and is accepted only by the explicit
// migration helper.
func NewSystemStore(_ string) (Store, error) {
	return newKeychainStore(keychainService, keychainAccount, systemKeychainBackend{}), nil
}
