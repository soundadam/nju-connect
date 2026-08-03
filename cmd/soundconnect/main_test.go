package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d", code)
	}
	if got := stdout.String(); got != "soundconnect dev\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPromptLineReadsTrimmedLocalInput(t *testing.T) {
	var output bytes.Buffer
	value, err := promptLine(bufio.NewReader(strings.NewReader("  vpn.example.edu  \n")), &output, "Gateway: ")
	if err != nil {
		t.Fatal(err)
	}
	if value != "vpn.example.edu" {
		t.Fatalf("value = %q", value)
	}
	if output.String() != "Gateway: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestPromptLineRejectsEmptyInput(t *testing.T) {
	var output bytes.Buffer
	if _, err := promptLine(bufio.NewReader(strings.NewReader("\n")), &output, "Account: "); err == nil {
		t.Fatal("promptLine() accepted empty input")
	}
}

func TestPromptLineDefaultAcceptsEmptyInput(t *testing.T) {
	var output bytes.Buffer
	value, err := promptLineDefault(bufio.NewReader(strings.NewReader("\n")), &output, "Gateway", "vpn.nju.edu.cn")
	if err != nil {
		t.Fatal(err)
	}
	if value != "vpn.nju.edu.cn" {
		t.Fatalf("value = %q", value)
	}
	if output.String() != "Gateway [vpn.nju.edu.cn]: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown) = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReleaseCommandsRejectWorktreeDevelopmentOverride(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"doctor", "--worktree", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(doctor --worktree) = %d", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined: -worktree") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

type commandMemoryStore struct {
	secret []byte
}

func (store *commandMemoryStore) Inspect() error {
	if store.secret == nil {
		return os.ErrNotExist
	}
	return nil
}

func (store *commandMemoryStore) Get() ([]byte, error) {
	if err := store.Inspect(); err != nil {
		return nil, err
	}
	return append([]byte(nil), store.secret...), nil
}

func (store *commandMemoryStore) Set(secret []byte) error {
	store.secret = append(store.secret[:0], secret...)
	return nil
}

func TestMigrateCommandCopiesConfigAndImportsCredential(t *testing.T) {
	legacyRoot := t.TempDir()
	legacy, err := config.LegacyPaths(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if err := config.Replace(legacy.Config, want); err != nil {
		t.Fatal(err)
	}
	legacyCredential, err := credential.NewFileStore(legacy.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyCredential.Set([]byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "soundconnect")
	destination := config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
	store := &commandMemoryStore{}
	previousPaths := resolveDefaultPaths
	previousStore := newSystemCredentialStore
	resolveDefaultPaths = func() (config.Paths, error) { return destination, nil }
	newSystemCredentialStore = func(string) (credential.Store, error) { return store, nil }
	t.Cleanup(func() {
		resolveDefaultPaths = previousPaths
		newSystemCredentialStore = previousStore
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"migrate", "--from", legacyRoot}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(migrate) = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "configuration_migrated: true\ncredential_migrated: true\nsource_preserved: true\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	got, err := config.Load(destination.Config)
	if err != nil || got != want {
		t.Fatalf("configuration = %#v, err = %v", got, err)
	}
	if string(store.secret) != "synthetic-password" {
		t.Fatal("credential was not imported")
	}
}
