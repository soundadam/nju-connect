// Package credential obtains the long-lived soundconnect login credential without
// putting it in command arguments or configuration. SMS and TOTP codes are
// deliberately outside this package because they are one-time authentication
// inputs and must not be persisted.
package credential

import (
	"errors"
)

const maxCredentialBytes = 1 << 20

var (
	ErrPlaintextOptIn      = errors.New("plaintext credential file requires explicit opt-in")
	ErrNotRegular          = errors.New("credential path is not a regular file")
	ErrInsecurePermissions = errors.New("credential file permissions are broader than 0600")
	ErrWrongOwner          = errors.New("credential path is not owned by the current user")
	ErrInsecureDirectory   = errors.New("credential directory must be private")
	ErrEmptyCredential     = errors.New("credential is empty")
	ErrCredentialTooLarge  = errors.New("credential exceeds the size limit")
	ErrNoTerminal          = errors.New("hidden prompt requires a terminal")
)

func validateSecret(secret []byte) error {
	if len(secret) == 0 {
		return ErrEmptyCredential
	}
	if len(secret) > maxCredentialBytes {
		return ErrCredentialTooLarge
	}
	return nil
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

// Clear overwrites a temporary secret buffer owned by the caller.
func Clear(value []byte) {
	clearBytes(value)
}
