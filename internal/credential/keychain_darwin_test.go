//go:build darwin && cgo

package credential

import (
	"errors"
	"os"
	"testing"
)

type fakeKeychainBackend struct {
	secret []byte
	err    error
}

func (backend *fakeKeychainBackend) inspect(_, _ string) error {
	if backend.err != nil {
		return backend.err
	}
	if backend.secret == nil {
		return os.ErrNotExist
	}
	return nil
}

func (backend *fakeKeychainBackend) get(_, _ string) ([]byte, error) {
	if err := backend.inspect("", ""); err != nil {
		return nil, err
	}
	return append([]byte(nil), backend.secret...), nil
}

func (backend *fakeKeychainBackend) set(_, _ string, secret []byte) error {
	if backend.err != nil {
		return backend.err
	}
	backend.secret = append(backend.secret[:0], secret...)
	return nil
}

func (backend *fakeKeychainBackend) delete(_, _ string) error {
	if backend.err != nil {
		return backend.err
	}
	if backend.secret == nil {
		return os.ErrNotExist
	}
	backend.secret = nil
	return nil
}

func TestKeychainStoreRoundTripUsesBackendWithoutExposingSecretMetadata(t *testing.T) {
	backend := &fakeKeychainBackend{}
	store := newKeychainStore("service", "account", backend)
	if err := store.Set([]byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}
	if err := store.Inspect(); err != nil {
		t.Fatal(err)
	}
	secret, err := store.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer Clear(secret)
	if string(secret) != "synthetic-password" {
		t.Fatal("Keychain round trip changed the credential")
	}
	if store.service != "service" || store.account != "account" {
		t.Fatal("Keychain identifiers changed")
	}
}

func TestKeychainStorePreservesNotFoundAndBackendErrors(t *testing.T) {
	store := newKeychainStore("service", "account", &fakeKeychainBackend{})
	if _, err := store.Get(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get() error = %v", err)
	}

	want := errors.New("Keychain locked")
	store = newKeychainStore("service", "account", &fakeKeychainBackend{err: want})
	if err := store.Set([]byte("synthetic-password")); !errors.Is(err, want) {
		t.Fatalf("Set() error = %v", err)
	}
}

func TestKeychainStoreClearIsIdempotent(t *testing.T) {
	backend := &fakeKeychainBackend{}
	store := newKeychainStore("service", "account", backend)
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() missing item error = %v", err)
	}
	if err := store.Set([]byte("synthetic-session")); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if backend.secret != nil {
		t.Fatal("Clear() retained the Keychain value")
	}
}

func TestLegacyKeychainItemsExistOnlyForTheDefaultService(t *testing.T) {
	for account, legacyAccount := range map[string]string{
		PasswordAccount: "vpn-password", ATrustSessionAccount: "atrust-client-data",
	} {
		store, ok := legacyKeychainStore(Location{Service: DefaultKeyringService, Account: account}).(*KeychainStore)
		if !ok || store.service != LegacyKeyringService || store.account != legacyAccount {
			t.Fatalf("legacy store for %s = %+v", account, store)
		}
		if legacy := legacyKeychainStore(Location{Service: KeyringService("/isolated", true), Account: account}); legacy != nil {
			t.Fatalf("isolated service reaches the real legacy item: %+v", legacy)
		}
	}
}

func TestKeychainStatusExplainsCancelledAuthorization(t *testing.T) {
	err := keychainStatus("read", errSecUserCanceled)
	if !errors.Is(err, ErrKeychainAccessCanceled) {
		t.Fatalf("keychainStatus() error = %v", err)
	}
}
