//go:build darwin && cgo

package credential

const (
	keychainService = "com.soundadam.soundconnect"
	// EasyConnect and aTrust password authentication intentionally share this item.
	keychainAccount         = "vpn-password"
	aTrustClientDataAccount = "atrust-client-data"
)

// NewSystemStore uses the current user's login Keychain on macOS. The legacy
// path is intentionally ignored here and is accepted only by the explicit
// migration helper.
func NewSystemStore(_ string) (Store, error) {
	return newKeychainStore(keychainService, keychainAccount, systemKeychainBackend{}), nil
}

// NewATrustClientDataStore stores the opaque aTrust session material in a
// separate Keychain generic-password item. The path is accepted for parity
// with the non-macOS fallback and is deliberately ignored on macOS.
func NewATrustClientDataStore(_ string) (Store, error) {
	return newKeychainStore(keychainService, aTrustClientDataAccount, systemKeychainBackend{}), nil
}
