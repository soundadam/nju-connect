package main

import (
	"bytes"
	"io"
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

func TestHelpPrintsUsageOnStdoutAndSucceeds(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) = %d", code)
	}
	if !strings.Contains(stdout.String(), "usage: soundconnect") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "observe") {
		t.Fatalf("usage advertises an unimplemented command: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSubcommandHelpRequestPrintsFlagsOnStdoutAndSucceeds(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"status", "-h"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(status -h) = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage of soundconnect status:") || !strings.Contains(stdout.String(), "-json") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUnimplementedObserveCommandIsNotDispatched(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"observe"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(observe) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "observe"`) {
		t.Fatalf("stderr = %q", stderr.String())
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

func TestDefaultAndConnectDispatchToRuntimeWhileDryRunStaysExplicit(t *testing.T) {
	previousConnect := connectCommand
	previousDryRun := dryRunCommand
	t.Cleanup(func() {
		connectCommand = previousConnect
		dryRunCommand = previousDryRun
	})

	connectCalls := 0
	dryRunCalls := 0
	connectCommand = func(arguments []string, _, _ io.Writer) int {
		connectCalls++
		if len(arguments) != 0 {
			t.Fatalf("connect arguments = %v", arguments)
		}
		return 17
	}
	dryRunCommand = func(arguments []string, _, _ io.Writer) int {
		dryRunCalls++
		if len(arguments) != 0 {
			t.Fatalf("dry-run arguments = %v", arguments)
		}
		return 23
	}

	var output bytes.Buffer
	if code := run(nil, &output, &output); code != 17 {
		t.Fatalf("run(default) = %d", code)
	}
	if code := run([]string{"connect"}, &output, &output); code != 17 {
		t.Fatalf("run(connect) = %d", code)
	}
	if code := run([]string{"dry-run"}, &output, &output); code != 23 {
		t.Fatalf("run(dry-run) = %d", code)
	}
	if connectCalls != 2 || dryRunCalls != 1 {
		t.Fatalf("connect calls = %d, dry-run calls = %d", connectCalls, dryRunCalls)
	}
}

func TestNativeConnectCommandWasReplaced(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"native-connect"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(native-connect) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "native-connect"`) {
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
