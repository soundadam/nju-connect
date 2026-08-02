package setup

import (
	"os"
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestSaveWritesPrivateConfigAndCredential(t *testing.T) {
	paths, err := config.LocalPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured := config.Config{
		Server:      "vpn.example.edu",
		Username:    "student",
		SOCKSListen: config.DefaultSOCKSListen,
	}
	readCount := 0
	err = Save(paths, configured, func(prompt string) ([]byte, error) {
		readCount++
		if prompt != "soundconnect password: " {
			t.Fatalf("prompt = %q", prompt)
		}
		return []byte("synthetic-password"), nil
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if readCount != 1 {
		t.Fatalf("secret reader called %d times, want 1", readCount)
	}
	loaded, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != configured {
		t.Fatalf("config = %#v", loaded)
	}
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
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
