package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestATrustConnectFailsCleanlyWithoutProtocolCore(t *testing.T) {
	for _, test := range []struct {
		name        string
		authType    string
		loginDomain string
	}{
		{name: "discovery", authType: atrustPasswordAuthType},
		{name: "oauth", authType: atrustOAuthAuthType, loginDomain: "tenant-oauth"},
		{name: "known-domain", authType: atrustPasswordAuthType, loginDomain: "openldap13924"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := useATrustTestState(t)
			writeATrustTestConfig(t, paths, test.authType, test.loginDomain)
			var stdout, stderr bytes.Buffer
			code := runNativeConnectContext(context.Background(), nil, &stdout, &stderr, newProductionNativeSession, nil)
			if code != 1 || !strings.Contains(stderr.String(), "aTrust protocol support is not available in this build") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if strings.Count(stderr.String(), "not available") != 1 {
				t.Fatalf("message repeated: %q", stderr.String())
			}
		})
	}
}

func TestATrustConnectRejectsBackgroundRuntime(t *testing.T) {
	paths := useATrustTestState(t)
	writeATrustTestConfig(t, paths, atrustPasswordAuthType, "")
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

func TestSetupATrustRecordsRequestedMethodWithoutProtocolCore(t *testing.T) {
	paths := useATrustTestState(t)
	var stdout, stderr bytes.Buffer
	code := runSetup([]string{"--backend", "atrust", "--server", "vpn.nju.edu.cn", "--auth-type", atrustOAuthAuthType}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	got, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendName() != backend.ATrust || got.AuthType != atrustOAuthAuthType || got.LoginDomain != "" {
		t.Fatalf("config = %#v", got)
	}
	if !strings.Contains(stdout.String(), "credential: browser_oauth") || !strings.Contains(stderr.String(), "discovery is unavailable") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(paths.Credential); !os.IsNotExist(err) {
		t.Fatalf("OAuth setup wrote a password: %v", err)
	}
}

func TestSetupRejectsATrustFlagsForEasyConnect(t *testing.T) {
	useATrustTestState(t)
	var stdout, stderr bytes.Buffer
	if code := runSetup([]string{"--auth-type", atrustPasswordAuthType}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestAuthInfoReportsMissingProtocolCore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runAuthInfo(nil, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), atrustbackend.ErrProtocolNotImplemented.Error()) {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
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
	if code := runLogout(nil, &stdout, &stderr); code != 0 {
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

func TestReadBoundedLineRejectsOversizedCallback(t *testing.T) {
	if line, err := readBoundedLine(strings.NewReader("https://a/\r\n"), 64); err != nil || line != "https://a/" {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if _, err := readBoundedLine(strings.NewReader(strings.Repeat("a", 65)+"\n"), 64); err == nil {
		t.Fatal("oversized callback accepted")
	}
}
