package app

import (
	"errors"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
)

func TestBackendsCatalog(t *testing.T) {
	catalog := Backends()
	if catalog.SchemaVersion != 1 || catalog.SOCKSListen != config.DefaultSOCKSListen {
		t.Fatalf("catalog = %+v", catalog)
	}
	if len(catalog.Backends) != 2 || catalog.Backends[0].ID != backend.EasyConnect || catalog.Backends[1].ID != backend.ATrust {
		t.Fatalf("backends = %+v", catalog.Backends)
	}
}

func TestDoctorReportsReadinessWithoutReadingSecrets(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	report, err := Doctor(env.deps)
	if err != nil || report.Ready || report.Configuration != "missing" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	env.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	env.setSecret(env.paths.Credential, "synthetic-password")
	report, err = Doctor(env.deps)
	if err != nil || !report.Ready || report.CredentialStore != "ready" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestDoctorWrapsDependencyFailures(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	failure := errors.New("boom")
	env.deps.PasswordStore = func(string) (credential.Store, error) { return nil, failure }
	if _, err := Doctor(env.deps); !errors.Is(err, failure) || err.Error() != "prepare credential store: boom" {
		t.Fatalf("err = %v", err)
	}
	env.deps.Paths = func() (config.Paths, error) { return config.Paths{}, failure }
	if _, err := Doctor(env.deps); err == nil || err.Error() != "resolve local state: boom" {
		t.Fatalf("err = %v", err)
	}
}
