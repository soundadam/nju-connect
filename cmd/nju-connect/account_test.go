//go:build linux || darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
)

func TestFlowAccountShow(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("account_show_missing", harness.run("account", "show").expect(t, 0))
	harness.writeConfig(config.Config{
		Backend: backend.ATrust, Server: "vpn.nju.edu.cn", Username: "student", SOCKSListen: config.DefaultSOCKSListen,
		AuthType: "auth/psw", LoginDomain: "ldap-domain",
	})
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.golden("account_show_text", harness.run("account").expect(t, 0))
	result := harness.run("account", "show", "--json").expect(t, 0)
	harness.golden("account_show_json", result)
	if result.stdout != `{"schema_version":1,"configuration":"ready","backend":"atrust","server":"vpn.nju.edu.cn","username":"student","auth_type":"auth/psw","credential_store":"keyring","password":"saved","atrust_session":"missing"}`+"\n" {
		t.Fatalf("stdout = %q", result.stdout)
	}
}

func TestFlowAccountSetPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: "127.0.0.1:1090",
		UpstreamProxy: "socks5://127.0.0.1:7890"})
	harness.writeSecret(harness.paths.Credential, "old-password")
	harness.stdin("new-password\n")
	harness.golden("account_set_password", harness.run("account", "set-password", "--password-stdin").expect(t, 0))
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "new-password" {
		t.Fatalf("password = %q, %v", secret, err)
	}
	if got, err := config.Load(harness.paths.Config); err != nil || got.UpstreamProxy != "socks5://127.0.0.1:7890" {
		t.Fatalf("configuration changed: %+v, %v", got, err)
	}

	harness.stdin("\n")
	harness.golden("account_set_password_empty", harness.run("account", "set-password", "--password-stdin").expect(t, 1))
	harness.stdin("new-password\n")
	harness.golden("account_set_password_without_terminal", harness.run("account", "set-password").expect(t, 1))
}

func TestFlowAccountSetUsernameForgetsTheSession(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("account_set_username_without_configuration", harness.run("account", "set-username", "student").expect(t, 1))
	harness.writeConfig(config.Config{
		Backend: backend.ATrust, Server: "vpn.nju.edu.cn", Username: "student", SOCKSListen: config.DefaultSOCKSListen,
		AuthType: "auth/psw", LoginDomain: "ldap-domain",
	})
	harness.writeSecret(harness.paths.ATrustClientData, "synthetic-client-data")
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.golden("account_set_username", harness.run("account", "set-username", "classmate").expect(t, 0))
	if got, err := config.Load(harness.paths.Config); err != nil || got.Username != "classmate" || got.LoginDomain != "ldap-domain" {
		t.Fatalf("configuration = %+v, %v", got, err)
	}
	if _, err := os.Lstat(harness.paths.ATrustClientData); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aTrust session kept for another account: %v", err)
	}
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password changed: %q, %v", secret, err)
	}
	harness.golden("account_set_username_usage", harness.run("account", "set-username").expect(t, 2))
}

func TestFlowAccountForget(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.writeSecret(harness.paths.ATrustClientData, "synthetic-client-data")
	helper := filepath.Join(t.TempDir(), "oauth-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$1\" = --clear-data ]\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NJU_CONNECT_ATRUST_OAUTH_HELPER", helper)

	harness.golden("account_forget_usage", harness.run("account", "forget").expect(t, 2))
	harness.golden("account_forget_session", harness.run("account", "forget", "--session").expect(t, 0))
	if _, err := harness.readSecret(harness.paths.Credential); err != nil {
		t.Fatalf("forgetting the session touched the password: %v", err)
	}
	harness.golden("account_forget_both", harness.run("account", "forget", "--password", "--session").expect(t, 0))
	for _, path := range []string{harness.paths.Credential, harness.paths.ATrustClientData} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains: %v", path, err)
		}
	}
	harness.golden("account_unknown_command", harness.run("account", "rename").expect(t, 2))
}

func TestFlowAccountChangesRefuseLiveRuntime(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.serveStatus(runtimecontrol.Snapshot{
		SchemaVersion: runtimecontrol.SchemaVersion, Running: true, State: "connected",
		Profile: runtime.ProfileCommunityUTLSCompat, SOCKSListen: config.DefaultSOCKSListen, AccessEvidence: "available",
	})
	harness.stdin("new-password\n")
	harness.golden("account_set_password_live_runtime", harness.run("account", "set-password", "--password-stdin").expect(t, 1))
	harness.run("account", "set-username", "classmate").expect(t, 1)
	harness.run("account", "forget", "--password").expect(t, 1)
	harness.stdin("synthetic-password\n")
	harness.golden("setup_live_runtime", harness.run("setup", "--server", "vpn.example.edu", "--username", "student", "--password-stdin").expect(t, 1))
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password changed while a runtime was active: %q, %v", secret, err)
	}
}
