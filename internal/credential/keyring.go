package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/zalando/go-keyring"
)

// DefaultKeyringService names soundconnect's items in the operating-system
// keyring: the macOS login Keychain, the Secret Service on Linux, or the
// Windows Credential Manager.
const DefaultKeyringService = "com.soundadam.soundconnect"

// Keyring accounts. EasyConnect and aTrust password authentication share
// PasswordAccount.
const (
	PasswordAccount      = "password"
	ATrustSessionAccount = "atrust-session"
)

// KeyringService returns the keyring service for a state directory. The
// default directory uses DefaultKeyringService; an isolated directory
// (SOUNDCONNECT_CONFIG_DIR) gets its own service so tests and alternate
// profiles never touch the real items.
func KeyringService(root string, isolated bool) string {
	if !isolated {
		return DefaultKeyringService
	}
	digest := sha256.Sum256([]byte(root))
	return DefaultKeyringService + "." + hex.EncodeToString(digest[:6])
}

var ErrKeyringUnavailable = errors.New("system keyring is unavailable")

const (
	inlinePrefix = "sc1:"
	chunkPrefix  = "sc1-chunks:"
	// maximumChunkEncoded keeps each item inside every platform's limit:
	// 2560 bytes on Windows, and about 3000 bytes on macOS once go-keyring
	// base64-encodes the value again for /usr/bin/security.
	maximumChunkEncoded = 2000
	maximumChunkRaw     = maximumChunkEncoded / 4 * 3
	maximumChunks       = (maxCredentialBytes + maximumChunkRaw - 1) / maximumChunkRaw
)

// KeyringStore keeps one secret in the system keyring. A secret that does
// not fit one item is split across chunk items named account#generation.N;
// the item named account holds either the whole secret or a header naming
// the current generation, so a rewrite never leaves a half-updated secret.
//
// go-keyring moves values as Go strings, which cannot be cleared; secrets
// therefore linger in memory until collected, as with any keyring client.
type KeyringStore struct {
	service string
	account string
}

// NewKeyringStore returns the store for one service and account.
func NewKeyringStore(service, account string) (*KeyringStore, error) {
	if service == "" || account == "" || strings.Contains(account, "#") {
		return nil, errors.New("keyring service and account are required")
	}
	return &KeyringStore{service: service, account: account}, nil
}

func (store *KeyringStore) Inspect() error {
	_, err := store.header()
	return err
}

func (store *KeyringStore) Get() ([]byte, error) {
	header, err := store.header()
	if err != nil {
		return nil, err
	}
	if encoded, inline := strings.CutPrefix(header, inlinePrefix); inline {
		return decodeKeyringValue(encoded)
	}
	generation, count, err := parseChunkHeader(header)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 0, count*maximumChunkRaw)
	for index := range count {
		value, err := store.get(store.chunkAccount(generation, index))
		if errors.Is(err, os.ErrNotExist) {
			err = errors.New("read keyring credential: a chunk is missing")
		}
		if err != nil {
			clearBytes(secret)
			return nil, err
		}
		encoded, ok := strings.CutPrefix(value, inlinePrefix)
		if !ok {
			clearBytes(secret)
			return nil, errors.New("read keyring credential: a chunk is malformed")
		}
		part, err := decodeKeyringValue(encoded)
		if err != nil {
			clearBytes(secret)
			return nil, err
		}
		secret = append(secret, part...)
		clearBytes(part)
	}
	if err := validateSecret(secret); err != nil {
		clearBytes(secret)
		return nil, err
	}
	return secret, nil
}

func (store *KeyringStore) Set(secret []byte) error {
	if err := validateSecret(secret); err != nil {
		return err
	}
	previous, _ := store.header()
	if len(secret) <= maximumChunkRaw {
		if err := store.set(store.account, inlinePrefix+base64.StdEncoding.EncodeToString(secret)); err != nil {
			return err
		}
		store.deleteChunks(previous)
		return nil
	}

	generation, err := newGeneration()
	if err != nil {
		return err
	}
	count := 0
	for offset := 0; offset < len(secret); offset += maximumChunkRaw {
		part := secret[offset:min(offset+maximumChunkRaw, len(secret))]
		if err := store.set(store.chunkAccount(generation, count), inlinePrefix+base64.StdEncoding.EncodeToString(part)); err != nil {
			store.deleteChunks(chunkPrefix + generation + ":" + strconv.Itoa(count+1))
			return err
		}
		count++
	}
	if err := store.set(store.account, chunkPrefix+generation+":"+strconv.Itoa(count)); err != nil {
		store.deleteChunks(chunkPrefix + generation + ":" + strconv.Itoa(count))
		return err
	}
	store.deleteChunks(previous)
	return nil
}

// Clear removes the secret and every chunk. A missing secret is success.
func (store *KeyringStore) Clear() error {
	header, err := store.header()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil && !errors.Is(err, errMalformedHeader) {
		return err
	}
	store.deleteChunks(header)
	if err := keyringError("delete", keyring.Delete(store.service, store.account)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

var errMalformedHeader = errors.New("read keyring credential: saved value is malformed")

func (store *KeyringStore) header() (string, error) {
	header, err := store.get(store.account)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(header, inlinePrefix) && !strings.HasPrefix(header, chunkPrefix) {
		return header, errMalformedHeader
	}
	return header, nil
}

func (store *KeyringStore) get(account string) (string, error) {
	value, err := keyring.Get(store.service, account)
	return value, keyringError("read", err)
}

func (store *KeyringStore) set(account, value string) error {
	return keyringError("store", keyring.Set(store.service, account, value))
}

func (store *KeyringStore) chunkAccount(generation string, index int) string {
	return fmt.Sprintf("%s#%s.%d", store.account, generation, index)
}

// deleteChunks removes the chunk items a header names. It is best effort:
// a leftover chunk holds only part of a superseded secret.
func (store *KeyringStore) deleteChunks(header string) {
	generation, count, err := parseChunkHeader(header)
	if err != nil {
		return
	}
	for index := range count {
		_ = keyring.Delete(store.service, store.chunkAccount(generation, index))
	}
}

func parseChunkHeader(header string) (string, int, error) {
	rest, ok := strings.CutPrefix(header, chunkPrefix)
	if !ok {
		return "", 0, errMalformedHeader
	}
	generation, countText, ok := strings.Cut(rest, ":")
	count, err := strconv.Atoi(countText)
	if !ok || generation == "" || err != nil || count < 1 || count > maximumChunks {
		return "", 0, errMalformedHeader
	}
	return generation, count, nil
}

func decodeKeyringValue(encoded string) ([]byte, error) {
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxCredentialBytes {
		return nil, ErrCredentialTooLarge
	}
	secret, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errMalformedHeader
	}
	if err := validateSecret(secret); err != nil {
		clearBytes(secret)
		return nil, err
	}
	return secret, nil
}

func newGeneration() (string, error) {
	var generation [4]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return "", fmt.Errorf("store keyring credential: %w", err)
	}
	return hex.EncodeToString(generation[:]), nil
}

// keyringError maps go-keyring errors onto the package's errors: a missing
// item is os.ErrNotExist, and a cancelled macOS access prompt is
// ErrKeychainAccessCanceled.
func keyringError(operation string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return fmt.Errorf("%s keyring credential: %w", operation, os.ErrNotExist)
	case errors.Is(err, keyring.ErrSetDataTooBig):
		return fmt.Errorf("%s keyring credential: %w", operation, ErrCredentialTooLarge)
	}
	var exit *exec.ExitError
	if runtime.GOOS == "darwin" && errors.As(err, &exit) {
		// /usr/bin/security exits with the low byte of the OSStatus.
		switch exit.ExitCode() {
		case 44: // errSecItemNotFound
			return fmt.Errorf("%s keyring credential: %w", operation, os.ErrNotExist)
		case 128: // errSecUserCanceled
			return fmt.Errorf("%s Keychain credential: %w", operation, ErrKeychainAccessCanceled)
		}
		return fmt.Errorf("%s Keychain credential: %w", operation, err)
	}
	if runtime.GOOS == "linux" || runtime.GOOS == "freebsd" || runtime.GOOS == "openbsd" {
		return fmt.Errorf("%s keyring credential: %w: %v (set credential_store = \"file\" in config.toml to keep secrets in owner-only files instead)",
			operation, ErrKeyringUnavailable, err)
	}
	return fmt.Errorf("%s keyring credential: %w", operation, err)
}
