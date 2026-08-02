// Package credential obtains the long-lived soundconnect login credential without
// putting it in command arguments or configuration. SMS and TOTP codes are
// deliberately outside this package because they are one-time authentication
// inputs and must not be persisted.
package credential

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Backend identifies how the long-lived credential is obtained.
type Backend string

const (
	BackendPrompt Backend = "prompt"
	BackendFile   Backend = "file"

	maxCredentialBytes = 1 << 20
)

var (
	ErrUnknownBackend      = errors.New("unknown credential backend")
	ErrPlaintextOptIn      = errors.New("plaintext credential file requires explicit opt-in")
	ErrNotRegular          = errors.New("credential path is not a regular file")
	ErrInsecurePermissions = errors.New("credential file permissions are broader than 0600")
	ErrWrongOwner          = errors.New("credential path is not owned by the current user")
	ErrInsecureDirectory   = errors.New("credential directory must be private")
	ErrEmptyCredential     = errors.New("credential is empty")
	ErrCredentialTooLarge  = errors.New("credential exceeds the size limit")
	ErrNoTerminal          = errors.New("hidden prompt requires a terminal")
	ErrPersistenceDisabled = errors.New("credential backend does not persist values")
)

// Store retrieves a credential and, when supported, persists a replacement.
// Implementations never print the credential.
type Store interface {
	Get() ([]byte, error)
	Set(secret []byte) error
}

// Options selects a credential backend. AllowPlaintextFile must be true in
// addition to selecting BackendFile.
type Options struct {
	Backend            Backend
	FilePath           string
	AllowPlaintextFile bool
	PromptInput        *os.File
	PromptOutput       io.Writer
}

// ParseBackend validates a configured backend name. An empty name defaults to
// the non-persistent hidden prompt.
func ParseBackend(value string) (Backend, error) {
	backend := Backend(strings.TrimSpace(value))
	if backend == "" {
		return BackendPrompt, nil
	}
	switch backend {
	case BackendPrompt, BackendFile:
		return backend, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownBackend, value)
	}
}

// Open constructs the selected backend without retrieving a credential.
func Open(options Options) (Store, error) {
	backend, err := ParseBackend(string(options.Backend))
	if err != nil {
		return nil, err
	}

	switch backend {
	case BackendPrompt:
		return NewPromptStore(PromptOptions{
			Input:  options.PromptInput,
			Output: options.PromptOutput,
		}), nil
	case BackendFile:
		return NewFileStore(options.FilePath, options.AllowPlaintextFile)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownBackend, backend)
	}
}

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
