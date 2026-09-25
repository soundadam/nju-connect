package app

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

// testEnv is an isolated state directory with owner-only file stores. It
// needs no environment variables, so tests using it can run in parallel.
type testEnv struct {
	t     *testing.T
	paths config.Paths
	deps  Deps
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{
		Root:             root,
		Config:           filepath.Join(root, "config.toml"),
		Credential:       filepath.Join(root, "credential"),
		ATrustClientData: filepath.Join(root, "atrust-client-data"),
	}
	fileStore := func(location credential.Location) (credential.Store, error) {
		return credential.NewFileStore(location.File, true)
	}
	return &testEnv{t: t, paths: paths, deps: Deps{
		Paths:              func() (config.Paths, error) { return paths, nil },
		PasswordStore:      fileStore,
		ATrustSessionStore: fileStore,
		ATrustCore:         func() atrustbackend.Core { t.Fatal("unexpected aTrust core"); return nil },
		OAuthHelper:        func() (string, bool) { return "", false },
		Interaction:        NewLineInteraction(LineOptions{Output: io.Discard}),
		Diagnostics:        io.Discard,
	}}
}

func (env *testEnv) writeConfig(configured config.Config) {
	env.t.Helper()
	if err := config.Replace(env.paths.Config, configured); err != nil {
		env.t.Fatal(err)
	}
}

func (env *testEnv) setSecret(path, value string) {
	env.t.Helper()
	store, err := credential.NewFileStore(path, true)
	if err != nil {
		env.t.Fatal(err)
	}
	if err := store.Set([]byte(value)); err != nil {
		env.t.Fatal(err)
	}
}

func (env *testEnv) secret(path string) (string, error) {
	store, err := credential.NewFileStore(path, true)
	if err != nil {
		return "", err
	}
	value, err := store.Get()
	return string(value), err
}

func TestUsageErrorWrapsItsCause(t *testing.T) {
	cause := errors.New("bad backend")
	err := Usagef("select protocol backend: %w", cause)
	if !IsUsage(err) || !errors.Is(err, cause) || err.Error() != "select protocol backend: bad backend" {
		t.Fatalf("err = %v", err)
	}
	if IsUsage(cause) {
		t.Fatal("plain error reported as usage error")
	}
}
