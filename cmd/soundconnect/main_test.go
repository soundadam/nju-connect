package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"version"}, &stdout, &stderr); code != 0 {
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

	if code := run(isolatedDeps(t), []string{"--help"}, &stdout, &stderr); code != 0 {
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

	if code := run(isolatedDeps(t), []string{"status", "-h"}, &stdout, &stderr); code != 0 {
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

	if code := run(isolatedDeps(t), []string{"observe"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(observe) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "observe"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"unknown"}, &stdout, &stderr); code != 2 {
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
	paths, deps := useATrustTestState(t)
	core := useATrustTestCore(t, &deps)
	core.methods = nil
	writeATrustTestConfig(t, paths, app.ATrustPasswordAuthType, "")

	for _, arguments := range [][]string{nil, {"connect"}} {
		var stdout, stderr bytes.Buffer
		if code := run(deps, arguments, &stdout, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "select aTrust authentication") {
			t.Fatalf("run(%v) = %d, stderr = %q", arguments, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(deps, []string{"dry-run", "-h"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "soundconnect dry-run") {
		t.Fatalf("run(dry-run -h) = %d, stdout = %q", code, stdout.String())
	}
}

func TestNativeConnectCommandWasReplaced(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(isolatedDeps(t), []string{"native-connect"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(native-connect) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "native-connect"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReleaseCommandsRejectWorktreeDevelopmentOverride(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(isolatedDeps(t), []string{"doctor", "--worktree", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(doctor --worktree) = %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag: --worktree") {
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
	deps := testDeps(t, destination)
	deps.PasswordStore = func(credential.Location) (credential.Store, error) { return store, nil }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(deps, []string{"migrate", "--from", legacyRoot}, &stdout, &stderr); code != 0 {
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

func TestUpstreamDebugLogNeedsSoundConnectDebug(t *testing.T) {
	stderr := &bytes.Buffer{}
	for value, want := range map[string]bool{"": false, "0": false, "true": false, "1": true} {
		getenv := func(name string) string {
			if name == "SOUNDCONNECT_DEBUG" {
				return value
			}
			return ""
		}
		if got := upstreamDebugLog(getenv, stderr); (got == stderr) != want || (got != nil) != want {
			t.Errorf("SOUNDCONNECT_DEBUG=%q: debug log = %v, want enabled %v", value, got, want)
		}
	}
}
