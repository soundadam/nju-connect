package credential

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func init() {
	keyring.MockInit()
}

func keyringStore(t *testing.T, account string) *KeyringStore {
	t.Helper()
	store, err := NewKeyringStore(KeyringService(t.TempDir(), true), account)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestKeyringServiceIsolatesConfigDirectories(t *testing.T) {
	if got := KeyringService("/Users/me/Library/Application Support/soundconnect", false); got != DefaultKeyringService {
		t.Fatalf("default service = %q", got)
	}
	first, second := KeyringService("/tmp/a", true), KeyringService("/tmp/b", true)
	if first == second || !strings.HasPrefix(first, DefaultKeyringService+".") || first != KeyringService("/tmp/a", true) {
		t.Fatalf("isolated services = %q, %q", first, second)
	}
}

func TestKeyringStoreRoundTripsAndClears(t *testing.T) {
	store := keyringStore(t, PasswordAccount)
	if err := store.Inspect(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Inspect() on empty store = %v", err)
	}
	if _, err := store.Get(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get() on empty store = %v", err)
	}
	// Binary data survives: the aTrust client data is opaque.
	secret := []byte("synthetic-\x00\xffpassword\n")
	if err := store.Set(secret); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Get() = %q, %v", got, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Inspect(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Inspect() after Clear = %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("second Clear() = %v", err)
	}
	if err := store.Set(nil); !errors.Is(err, ErrEmptyCredential) {
		t.Fatalf("Set(nil) = %v", err)
	}
}

func chunkItems(t *testing.T, store *KeyringStore) int {
	t.Helper()
	header, err := keyring.Get(store.service, store.account)
	if err != nil {
		t.Fatal(err)
	}
	generation, count, err := parseChunkHeader(header)
	if err != nil {
		return 0
	}
	for index := range count {
		if _, err := keyring.Get(store.service, store.chunkAccount(generation, index)); err != nil {
			t.Fatalf("chunk %d: %v", index, err)
		}
	}
	return count
}

func TestKeyringStoreChunksLargeSecrets(t *testing.T) {
	store := keyringStore(t, ATrustSessionAccount)
	large := bytes.Repeat([]byte("0123456789abcdef"), 512) // 8 KiB
	if err := store.Set(large); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(); err != nil || !bytes.Equal(got, large) {
		t.Fatalf("Get() returned %d bytes, %v", len(got), err)
	}
	firstHeader, _ := keyring.Get(store.service, store.account)
	if count := chunkItems(t, store); count != 6 {
		t.Fatalf("chunks = %d", count)
	}

	// A rewrite switches to a new generation and removes the old chunks.
	larger := append(large, large...)
	if err := store.Set(larger); err != nil {
		t.Fatal(err)
	}
	generation, count, _ := parseChunkHeader(firstHeader)
	for index := range count {
		if _, err := keyring.Get(store.service, store.chunkAccount(generation, index)); !errors.Is(err, keyring.ErrNotFound) {
			t.Fatalf("old chunk %d survived: %v", index, err)
		}
	}
	if got, err := store.Get(); err != nil || !bytes.Equal(got, larger) {
		t.Fatalf("Get() after rewrite returned %d bytes, %v", len(got), err)
	}

	// Shrinking back to one item removes every chunk too.
	secondHeader, _ := keyring.Get(store.service, store.account)
	if err := store.Set([]byte("small")); err != nil {
		t.Fatal(err)
	}
	if chunkItems(t, store) != 0 {
		t.Fatal("small secret still uses chunks")
	}
	generation, count, _ = parseChunkHeader(secondHeader)
	if _, err := keyring.Get(store.service, store.chunkAccount(generation, count-1)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("chunk survived shrinking: %v", err)
	}

	if err := store.Set(larger); err != nil {
		t.Fatal(err)
	}
	header, _ := keyring.Get(store.service, store.account)
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	generation, _, _ = parseChunkHeader(header)
	if _, err := keyring.Get(store.service, store.chunkAccount(generation, 0)); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("Clear left a chunk: %v", err)
	}
}

func TestKeyringStoreRejectsDamagedValues(t *testing.T) {
	store := keyringStore(t, PasswordAccount)
	for name, value := range map[string]string{
		"foreign":      "plain-text-password",
		"bad base64":   inlinePrefix + "***",
		"bad header":   chunkPrefix + "abc",
		"chunk count":  chunkPrefix + "abc:0",
		"missing part": chunkPrefix + "abc:2",
	} {
		if err := keyring.Set(store.service, store.account, value); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(); err == nil || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: Get() = %v", name, err)
		}
	}
	// A damaged value can still be cleared, so the user can start over.
	if err := keyring.Set(store.service, store.account, "plain-text-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestNewKeyringStoreRequiresIdentifiers(t *testing.T) {
	for _, pair := range [][2]string{{"", PasswordAccount}, {DefaultKeyringService, ""}, {DefaultKeyringService, "a#b"}} {
		if _, err := NewKeyringStore(pair[0], pair[1]); err == nil {
			t.Fatalf("NewKeyringStore(%q, %q) accepted", pair[0], pair[1])
		}
	}
}
