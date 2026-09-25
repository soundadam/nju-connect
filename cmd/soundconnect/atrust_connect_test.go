package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

// useATrustTestState points the CLI at a private temporary root and replaces
// every aTrust secret store with owner-only files so tests never touch the
// user's Keychain.
func useATrustTestState(t *testing.T) config.Paths {
	t.Helper()
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{
		Root:             root,
		Config:           filepath.Join(root, "config.toml"),
		Credential:       filepath.Join(root, "credential"),
		ATrustClientData: filepath.Join(root, "atrust-client-data"),
	}
	previousPaths := resolveDefaultPaths
	previousPassword := newSystemCredentialStore
	previousClientData := newATrustClientDataStore
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	newSystemCredentialStore = func(path string) (credential.Store, error) { return credential.NewFileStore(path, true) }
	newATrustClientDataStore = func(path string) (credential.Store, error) { return credential.NewFileStore(path, true) }
	t.Cleanup(func() {
		resolveDefaultPaths = previousPaths
		newSystemCredentialStore = previousPassword
		newATrustClientDataStore = previousClientData
	})
	return paths
}

func writeATrustTestConfig(t *testing.T, paths config.Paths, authType, loginDomain string) {
	t.Helper()
	if err := config.Replace(paths.Config, config.Config{
		Backend:     backend.ATrust,
		Server:      config.DefaultATrustServer,
		Username:    "student",
		SOCKSListen: "127.0.0.1:0",
		AuthType:    authType,
		LoginDomain: loginDomain,
	}); err != nil {
		t.Fatal(err)
	}
}

// cliTestCore is a protocol core whose discovery advertises fixed methods
// and whose logins fail, so CLI tests never reach a gateway.
type cliTestCore struct {
	methods []backend.AuthenticationMethod
	logins  int
}

func (core *cliTestCore) Discover(context.Context, backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	return core.methods, nil
}

func (core *cliTestCore) Authenticate(context.Context, atrustbackend.LoginRequest, atrustbackend.Prompter) (atrustbackend.Session, error) {
	core.logins++
	return nil, errors.New("synthetic login failure")
}

func (core *cliTestCore) Resume(context.Context, atrustbackend.ResumeRequest) (atrustbackend.Session, error) {
	return nil, atrustbackend.ErrSessionExpired
}

func useATrustTestCore(t *testing.T) *cliTestCore {
	t.Helper()
	core := &cliTestCore{methods: []backend.AuthenticationMethod{
		{Domain: "openldap13924", Type: app.ATrustPasswordAuthType, Name: "Password"},
		{Domain: "tenant-oauth", Type: app.ATrustOAuthAuthType, Name: "OAuth", LoginURL: "https://vpn.nju.edu.cn/login"},
	}}
	previous := newATrustCore
	newATrustCore = func() atrustbackend.Core { return core }
	t.Cleanup(func() { newATrustCore = previous })
	return core
}

func TestATrustConnectReportsLoginFailureOnce(t *testing.T) {
	for _, test := range []struct {
		name        string
		authType    string
		loginDomain string
	}{
		{name: "discovery", authType: app.ATrustPasswordAuthType},
		{name: "known-domain", authType: app.ATrustPasswordAuthType, loginDomain: "openldap13924"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := useATrustTestState(t)
			core := useATrustTestCore(t)
			writeATrustTestConfig(t, paths, test.authType, test.loginDomain)
			var stdout, stderr bytes.Buffer
			code := runNativeConnectContext(context.Background(), nil, &stdout, &stderr, newProductionNativeSession, nil)
			if code != 1 || strings.Count(stderr.String(), "synthetic login failure") != 1 || core.logins != 1 {
				t.Fatalf("exit=%d logins=%d stdout=%q stderr=%q", code, core.logins, stdout.String(), stderr.String())
			}
		})
	}
}

func TestATrustConnectRejectsBackgroundRuntime(t *testing.T) {
	paths := useATrustTestState(t)
	writeATrustTestConfig(t, paths, app.ATrustPasswordAuthType, "")
	var stdout, stderr bytes.Buffer
	code := runNativeConnectContext(context.Background(), []string{"--background"}, &stdout, &stderr, nil,
		func(nativeapp.SessionConfig, string) (int, error) {
			t.Fatal("background starter called")
			return 0, nil
		})
	if code != 2 || !strings.Contains(stderr.String(), "aTrust background runtime is not implemented yet") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestSetupATrustRecordsDiscoveredMethod(t *testing.T) {
	paths := useATrustTestState(t)
	useATrustTestCore(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"setup", "--backend", "atrust", "--server", "vpn.nju.edu.cn", "--auth-type", app.ATrustOAuthAuthType}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	got, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendName() != backend.ATrust || got.AuthType != app.ATrustOAuthAuthType || got.LoginDomain != "tenant-oauth" {
		t.Fatalf("config = %#v", got)
	}
	if !strings.Contains(stdout.String(), "credential: browser_oauth") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(paths.Credential); !os.IsNotExist(err) {
		t.Fatalf("OAuth setup wrote a password: %v", err)
	}
}

func TestSetupRejectsATrustFlagsForEasyConnect(t *testing.T) {
	useATrustTestState(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"setup", "--auth-type", app.ATrustPasswordAuthType}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAuthInfoListsDiscoveredMethods(t *testing.T) {
	useATrustTestCore(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"auth-info"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "openldap13924") || !strings.Contains(stdout.String(), "tenant-oauth") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestLogoutClearsATrustClientDataAndOAuthProfile(t *testing.T) {
	paths := useATrustTestState(t)
	store, err := newATrustClientDataStore(paths.ATrustClientData)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte("synthetic-client-data")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "cleared")
	helper := filepath.Join(t.TempDir(), "helper")
	script := "#!/bin/sh\n[ \"$1\" = --clear-data ] && touch '" + marker + "'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", helper)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"logout"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if stdout.String() != "atrust_session_cleared: true\noauth_profile_cleared: true\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(paths.ATrustClientData); !os.IsNotExist(err) {
		t.Fatalf("client data remains: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("helper was not asked to clear its profile: %v", err)
	}
}
