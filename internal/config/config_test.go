package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
)

func TestReplaceAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.toml")
	want := Config{
		Server:            "vpn.example.edu",
		Username:          "student",
		SOCKSListen:       DefaultSOCKSListen,
		UpstreamProxy:     "socks5://127.0.0.1:1080",
		NativeTLSInsecure: true,
	}
	if err := Replace(path, want); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %04o", info.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestDefaultUsesNJUDefaultServer(t *testing.T) {
	configured := Default()
	if configured.Server != DefaultServer {
		t.Fatalf("Server = %q, want %q", configured.Server, DefaultServer)
	}
	if configured.SOCKSListen != DefaultSOCKSListen {
		t.Fatalf("SOCKSListen = %q, want %q", configured.SOCKSListen, DefaultSOCKSListen)
	}
	if configured.BackendName() != backend.EasyConnect {
		t.Fatalf("Backend = %q, want %q", configured.BackendName(), backend.EasyConnect)
	}
}

func TestATrustConfigDoesNotRequireUsername(t *testing.T) {
	configured := Config{
		Backend: backend.ATrust, Server: DefaultATrustServer,
		SOCKSListen: DefaultSOCKSListen, AuthType: "auth/httpsOauth2", LoginDomain: "tenant-oauth",
	}
	if err := configured.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	configured.LoginDomain = ""
	if err := configured.Validate(); err != nil {
		t.Fatalf("Validate() rejected a dynamically discovered login domain: %v", err)
	}
	configured.LoginDomain = "openldap13924"
	configured.AuthType = "auth/psw"
	if err := configured.Validate(); err == nil {
		t.Fatal("Validate() accepted aTrust password authentication without a username")
	}
	configured.Username = "student"
	if err := configured.Validate(); err != nil {
		t.Fatalf("Validate() rejected aTrust password authentication: %v", err)
	}
}

func TestCredentialStoreSetting(t *testing.T) {
	configured, err := Parse([]byte("username='student'\ncredential_store='file'\n"))
	if err != nil || configured.CredentialStore != "file" {
		t.Fatalf("Parse() = %+v, %v", configured, err)
	}
	if _, err := Parse([]byte("username='student'\ncredential_store='vault'\n")); err == nil {
		t.Fatal("Parse() accepted an unknown credential store")
	}
}

func TestParseRejectsSecretsAndUnknownFields(t *testing.T) {
	_, err := Parse([]byte("server='vpn.example.edu'\nusername='student'\npassword='secret'\n"))
	if err == nil {
		t.Fatal("Parse() accepted a password field")
	}
}

func TestLoadRejectsPermissiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := []byte("server='vpn.example.edu'\nusername='student'\nsocks_listen='127.0.0.1:1081'\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted permissive config")
	}
}

func TestValidateRequiresNumericLoopback(t *testing.T) {
	configured := Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: "localhost:1081"}
	if err := configured.Validate(); err == nil {
		t.Fatal("Validate() accepted a hostname listener")
	}
	configured.SOCKSListen = "127.0.0.1:1081"
	if err := configured.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoadMissingWrapsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v", err)
	}
}
