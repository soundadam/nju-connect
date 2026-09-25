//go:build !darwin || !cgo

package credential

// Only cgo macOS builds could have written the pre-keyring Keychain items.
func legacyKeychainStore(Location) Clearable { return nil }
