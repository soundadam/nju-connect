package doctor

import (
	"path/filepath"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
)

func TestBuildReportsReadyLocalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nju-connect")
	paths := config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
	configured := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}
	report := Build(paths, store)
	if !report.Ready || report.Configuration != "ready" || report.CredentialStore != "ready" || report.NextStep != NextConnect {
		t.Fatalf("report = %+v", report)
	}
}

func TestBuildDoesNotRequirePasswordForATrustOAuth(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nju-connect")
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml")}
	configured := config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer,
		SOCKSListen: config.DefaultSOCKSListen, AuthType: "auth/httpsOauth2",
	}
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	report := Build(paths, nil)
	if !report.Ready || report.CredentialStore != "not_required" {
		t.Fatalf("report = %+v", report)
	}

	configured.AuthType = "auth/psw"
	configured.Username = "student"
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	if report := Build(paths, nil); report.Ready || report.CredentialStore != "invalid" || report.NextStep != NextSetup {
		t.Fatalf("aTrust password report = %+v", report)
	}
}

func TestBuildNamesTheNextStep(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nju-connect")
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml"), Credential: filepath.Join(root, "credential")}
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	if report := Build(paths, store); report.NextStep != NextSetup {
		t.Fatalf("missing configuration: %+v", report)
	}
	configured := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	if report := Build(paths, store); report.NextStep != NextSetPassword {
		t.Fatalf("missing password: %+v", report)
	}
	if err := store.Set([]byte("synthetic-password")); err != nil {
		t.Fatal(err)
	}
	configured.UpstreamProxy = "socks5://127.0.0.1:1"
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	if report := Build(paths, store); report.NextStep != NextConfigureProxy {
		t.Fatalf("unavailable proxy: %+v", report)
	}
}
