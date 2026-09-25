package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

func TestAccountShowReportsSavedSecretsWithoutReadingThem(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	info, err := AccountShow(env.deps)
	want := AccountInfo{SchemaVersion: 1, Configuration: "missing", CredentialStore: "keyring", Password: SecretMissing, ATrustSession: SecretMissing}
	if err != nil || info != want {
		t.Fatalf("AccountShow() = %+v, %v", info, err)
	}

	env.writeConfig(config.Config{
		Backend: backend.ATrust, Server: "vpn.nju.edu.cn", SOCKSListen: config.DefaultSOCKSListen,
		AuthType: ATrustOAuthAuthType, CredentialStore: credential.BackendFile,
	})
	env.setSecret(env.paths.ATrustClientData, "client-data")
	env.deps.PasswordStore = func(location credential.Location) (credential.Store, error) {
		if location.Backend != credential.BackendFile || location.Account != credential.PasswordAccount {
			t.Errorf("password location = %+v", location)
		}
		return credential.NewFileStore(location.File, true)
	}
	info, err = AccountShow(env.deps)
	if err != nil || info.Password != SecretNotRequired || info.ATrustSession != SecretSaved || info.CredentialStore != "file" {
		t.Fatalf("AccountShow() = %+v, %v", info, err)
	}

	env.deps.ATrustSessionStore = func(credential.Location) (credential.Store, error) { return nil, errors.New("locked") }
	if info, _ = AccountShow(env.deps); info.ATrustSession != SecretUnavailable {
		t.Fatalf("ATrustSession = %q", info.ATrustSession)
	}
}

func TestSetUsernameKeepsTheSessionForTheSameAccount(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	env.setSecret(env.paths.ATrustClientData, "client-data")
	if cleared, err := SetUsername(env.deps, " student "); err != nil || cleared {
		t.Fatalf("SetUsername(same) = %t, %v", cleared, err)
	}
	if _, err := SetUsername(env.deps, "  "); !IsUsage(err) {
		t.Fatalf("SetUsername(blank) = %v", err)
	}
	if cleared, err := SetUsername(env.deps, "classmate"); err != nil || !cleared {
		t.Fatalf("SetUsername(other) = %t, %v", cleared, err)
	}
}

func TestForgetReportsWhatWasSaved(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.setSecret(env.paths.Credential, "synthetic-password")
	result, err := Forget(context.Background(), env.deps, ForgetRequest{Password: true, Session: true})
	if err != nil || result != (ForgetResult{PasswordForgotten: true}) {
		t.Fatalf("Forget() = %+v, %v", result, err)
	}
	if _, err := env.secret(env.paths.Credential); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("password remains: %v", err)
	}
	if result, err = Forget(context.Background(), env.deps, ForgetRequest{Password: true}); err != nil || result.PasswordForgotten {
		t.Fatalf("second Forget() = %+v, %v", result, err)
	}
}

func TestAccountMenu(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	// Change the password, rename the account, forget the password, done.
	output := env.answer("1\nnew-password\n2\nclassmate\n4\n\n", true)
	var summaries []AccountInfo
	if err := AccountMenu(context.Background(), env.deps, func(info AccountInfo) { summaries = append(summaries, info) }); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 4 || summaries[1].Password != SecretSaved || summaries[2].Username != "classmate" ||
		summaries[3].Password != SecretMissing {
		t.Fatalf("summaries = %+v", summaries)
	}
	if !strings.Contains(output.String(), "  4) Forget the saved password\n  5) Done\nAccount [done]: ") {
		t.Fatalf("prompts = %q", output.String())
	}
}
