package doctor

import (
	"path/filepath"
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestBuildReportsReadyLocalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "soundconnect")
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
	if !report.Ready || report.Configuration != "ready" || report.CredentialStore != "ready" {
		t.Fatalf("report = %+v", report)
	}
}
