package credential

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewFileStoreRequiresExplicitOptIn(t *testing.T) {
	if _, err := NewFileStore("credential", false); !errors.Is(err, ErrPlaintextOptIn) {
		t.Fatalf("NewFileStore() error = %v", err)
	}
}

func TestFileStoreAtomicallyWrites0600AndReadsExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "login.credential")
	store, err := NewFileStore(path, true)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}

	first := []byte("synthetic-first\n")
	if err := store.Set(first); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("mode = %v, want regular", info.Mode())
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("mode = %04o, want 0600", got)
	}
	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != string(first) {
		t.Fatal("Get() did not preserve exact bytes")
	}

	if runtime.GOOS != "windows" {
		openedOld, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open(old) error = %v", err)
		}
		defer openedOld.Close()

		second := []byte("synthetic-second")
		if err := store.Set(second); err != nil {
			t.Fatalf("replacement Set() error = %v", err)
		}
		oldInfo, err := openedOld.Stat()
		if err != nil {
			t.Fatalf("Stat(old) error = %v", err)
		}
		newInfo, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(new) error = %v", err)
		}
		if os.SameFile(oldInfo, newInfo) {
			t.Fatal("Set() modified the existing inode instead of atomically replacing it")
		}
		got, err = store.Get()
		if err != nil {
			t.Fatalf("Get() after replacement error = %v", err)
		}
		if string(got) != string(second) {
			t.Fatal("Get() after replacement returned unexpected bytes")
		}
	}

	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".nju-connect-credential-*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary credential files remain: %v", leftovers)
	}
}

func TestFileStoreRejectsPermissionsBroaderThan0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	original := []byte("synthetic-original")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	store, err := NewFileStore(path, true)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if _, err := store.Get(); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Get() error = %v, want ErrInsecurePermissions", err)
	}
	if err := store.Set([]byte("synthetic-replacement")); !errors.Is(err, ErrInsecurePermissions) {
		t.Fatalf("Set() error = %v, want ErrInsecurePermissions", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != string(original) {
		t.Fatal("rejected Set() changed existing file")
	}
}

func TestFileStoreRejectsSharedCredentialDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(directory, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0777); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(filepath.Join(directory, "credential"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte("synthetic")); !errors.Is(err, ErrInsecureDirectory) {
		t.Fatalf("Set() error = %v, want ErrInsecureDirectory", err)
	}
}

func TestFileStoreRejectsNonRegularPaths(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, true)
	if err != nil {
		t.Fatalf("NewFileStore(directory) error = %v", err)
	}
	if _, err := store.Get(); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Get(directory) error = %v, want ErrNotRegular", err)
	}
	if err := store.Set([]byte("synthetic")); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Set(directory) error = %v, want ErrNotRegular", err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("synthetic-target"), 0600); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Symlink() unavailable: %v", err)
	}
	linkStore, err := NewFileStore(link, true)
	if err != nil {
		t.Fatalf("NewFileStore(link) error = %v", err)
	}
	if _, err := linkStore.Get(); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Get(link) error = %v, want ErrNotRegular", err)
	}
	if err := linkStore.Set([]byte("synthetic")); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("Set(link) error = %v, want ErrNotRegular", err)
	}
}

func TestFileStoreRejectsEmptyAndOversizedCredentials(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "credential"), true)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	if err := store.Set(nil); !errors.Is(err, ErrEmptyCredential) {
		t.Fatalf("Set(empty) error = %v", err)
	}
	oversized := make([]byte, maxCredentialBytes+1)
	if err := store.Set(oversized); !errors.Is(err, ErrCredentialTooLarge) {
		t.Fatalf("Set(oversized) error = %v", err)
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("credential path exists after rejected writes: %v", err)
	}
}

func TestFileStoreClearRemovesCredentialAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credential")
	store, err := NewFileStore(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte("synthetic-session")); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("credential path after Clear() = %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("second Clear() error = %v", err)
	}
}
