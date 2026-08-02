package setup

import (
	"errors"
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
	answers := [][]byte{[]byte("synthetic-password"), []byte("synthetic-password")}
	index := 0
	err = Save(paths, configured, func(string) ([]byte, error) {
		answer := append([]byte(nil), answers[index]...)
		index++
		return answer, nil
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
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

func TestSaveRejectsPasswordMismatchWithoutWriting(t *testing.T) {
	paths, err := config.LocalPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configured := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	answers := [][]byte{[]byte("first"), []byte("second")}
	index := 0
	err = Save(paths, configured, func(string) ([]byte, error) {
		answer := append([]byte(nil), answers[index]...)
		index++
		return answer, nil
	})
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(paths.Config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config exists after mismatch: %v", err)
	}
}
