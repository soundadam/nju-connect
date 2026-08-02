package doctor

import (
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestBuildReportsReadyLocalState(t *testing.T) {
	paths, err := config.LocalPaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
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
	report := Build(paths)
	if !report.Ready || report.Configuration != "ready" || report.CredentialFile != "ready" {
		t.Fatalf("report = %+v", report)
	}
}
