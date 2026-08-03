package setup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestSaveWritesPrivateConfigAndCredential(t *testing.T) {
	paths := setupTestPaths(t)
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	configured := config.Config{
		Server:      "vpn.example.edu",
		Username:    "student",
		SOCKSListen: config.DefaultSOCKSListen,
	}
	readCount := 0
	err = Save(paths, configured, store, func(prompt string) ([]byte, error) {
		readCount++
		switch readCount {
		case 1:
			if prompt != "soundconnect password: " {
				t.Fatalf("prompt = %q", prompt)
			}
		case 2:
			if prompt != "soundconnect password (again): " {
				t.Fatalf("prompt = %q", prompt)
			}
		default:
			t.Fatalf("unexpected prompt %q", prompt)
		}
		return []byte("synthetic-password"), nil
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if readCount != 2 {
		t.Fatalf("secret reader called %d times, want 2", readCount)
	}
	loaded, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != configured {
		t.Fatalf("config = %#v", loaded)
	}
	secret, err := store.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer credential.Clear(secret)
	if string(secret) != "synthetic-password" {
		t.Fatal("stored password differs")
	}
	for _, path := range []string{paths.Config, paths.Credential} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %04o", path, info.Mode().Perm())
		}
	}
}

func TestSaveRejectsMismatchedPassword(t *testing.T) {
	paths := setupTestPaths(t)
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	configured := config.Config{
		Server:      "vpn.example.edu",
		Username:    "student",
		SOCKSListen: config.DefaultSOCKSListen,
	}
	err = Save(paths, configured, store, func(prompt string) ([]byte, error) {
		if prompt == "soundconnect password: " {
			return []byte("first"), nil
		}
		return []byte("second"), nil
	})
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Save() error = %v, want ErrPasswordMismatch", err)
	}
	if _, err := os.Stat(paths.Config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config exists after mismatch: %v", err)
	}
}

func setupTestPaths(t *testing.T) config.Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "soundconnect")
	return config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
}
