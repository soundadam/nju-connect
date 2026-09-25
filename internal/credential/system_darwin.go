//go:build darwin && cgo

package credential

// Pre-keyring releases stored secrets in these login Keychain items through
// the Security framework. They are read once, moved into the keyring and
// deleted; the real items exist only under DefaultKeyringService.
var legacyKeychainAccounts = map[string]string{
	PasswordAccount:      "vpn-password",
	ATrustSessionAccount: "atrust-client-data",
}

func legacyKeychainStore(location Location) Clearable {
	account, ok := legacyKeychainAccounts[location.Account]
	if !ok || location.Service != DefaultKeyringService {
		return nil
	}
	return newKeychainStore(DefaultKeyringService, account, systemKeychainBackend{})
}
