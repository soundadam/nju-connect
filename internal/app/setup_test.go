package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

// discoveryCore is an aTrust core that only answers public discovery.
type discoveryCore struct {
	atrustbackend.Core
	methods []backend.AuthenticationMethod
	err     error
}

func (core discoveryCore) Discover(context.Context, backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	return core.methods, core.err
}

var njuMethods = []backend.AuthenticationMethod{
	{Name: "Unified identity", Type: ATrustOAuthAuthType, Domain: "oauth-domain", LoginURL: "https://vpn.nju.edu.cn/login"},
	{Name: "Account password", Type: ATrustPasswordAuthType, Domain: "ldap-domain"},
}

func (env *testEnv) withDiscovery(methods []backend.AuthenticationMethod, err error) {
	env.deps.ATrustCore = func() atrustbackend.Core { return discoveryCore{methods: methods, err: err} }
}

// answer makes the interaction read content as piped standard input.
func (env *testEnv) answer(content string, passwordFromStdin bool) *strings.Builder {
	var output strings.Builder
	env.deps.Interaction = NewLineInteraction(LineOptions{
		Input: fileInput(env.t, content), Output: &output, PasswordFromStdin: passwordFromStdin,
	})
	return &output
}

func TestSetupEasyConnectAsksForMissingValues(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	output := env.answer("\nstudent\nsynthetic-password\n", true)
	result, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", SOCKSListen: config.DefaultSOCKSListen, PasswordSupplied: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != (SetupResult{Path: env.paths.Config, Backend: backend.EasyConnect, Credential: CredentialSystemStore}) {
		t.Fatalf("result = %+v", result)
	}
	if output.String() != "Gateway [vpn.nju.edu.cn]: Account: " {
		t.Fatalf("prompts = %q", output.String())
	}
	want := config.Config{
		Backend: backend.EasyConnect, Server: config.DefaultServer, Username: "student", SOCKSListen: config.DefaultSOCKSListen,
	}
	if got, err := config.Load(env.paths.Config); err != nil || got != want {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
	for _, path := range []string{env.paths.Config, env.paths.Credential} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v, %v", path, info, err)
		}
	}
}

func TestSetupATrustPasswordSuppliedSelectsPasswordAuthentication(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.withDiscovery(njuMethods, nil)
	env.answer("synthetic-password\n", true)
	result, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn", Username: "student",
		SOCKSListen: config.DefaultSOCKSListen, PasswordSupplied: true,
	})
	if err != nil || result.Credential != CredentialSystemStorePassword {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := config.Load(env.paths.Config)
	if err != nil || got.AuthType != ATrustPasswordAuthType || got.LoginDomain != "ldap-domain" {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
}

func TestSetupATrustOAuthStoresNoPassword(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.withDiscovery(njuMethods, nil)
	env.deps.PasswordStore = func(string) (credential.Store, error) {
		t.Fatal("OAuth setup opened the password store")
		return nil, nil
	}
	result, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn", SOCKSListen: config.DefaultSOCKSListen,
	})
	if err != nil || result.Credential != CredentialBrowserOAuth {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := config.Load(env.paths.Config)
	if err != nil || got.AuthType != ATrustOAuthAuthType || got.LoginDomain != "oauth-domain" {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
	if _, err := os.Stat(env.paths.Credential); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential written for OAuth: %v", err)
	}
}

func TestSetupATrustAsksForAccountOnlyForPasswordAuthentication(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.withDiscovery(njuMethods, nil)
	output := env.answer("student\nsynthetic-password\n", true)
	if _, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn", SOCKSListen: config.DefaultSOCKSListen, PasswordSupplied: true,
	}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Account: " {
		t.Fatalf("prompts = %q", output.String())
	}
}

func TestSetupFailures(t *testing.T) {
	t.Parallel()
	discoveryFailure := errors.New("gateway unreachable")
	for _, testCase := range []struct {
		name    string
		request SetupRequest
		methods []backend.AuthenticationMethod
		discErr error
		usage   bool
		message string
	}{
		{name: "unknown backend", request: SetupRequest{Backend: "openvpn"}, usage: true,
			message: "select protocol backend: unsupported protocol backend"},
		{name: "aTrust flags on EasyConnect", request: SetupRequest{Backend: "easyconnect", AuthType: ATrustPasswordAuthType}, usage: true,
			message: "auth-type and login-domain are only available for the aTrust backend"},
		{name: "bad aTrust gateway", request: SetupRequest{Backend: "atrust", Server: "vpn.example.edu:https"}, usage: true,
			message: "parse aTrust gateway: invalid port"},
		{name: "discovery failure", request: SetupRequest{Backend: "atrust", Server: "vpn.nju.edu.cn"}, discErr: discoveryFailure,
			message: "discover aTrust authentication: gateway unreachable"},
		{name: "method unavailable", request: SetupRequest{Backend: "atrust", Server: "vpn.nju.edu.cn", AuthType: ATrustPasswordAuthType},
			methods: njuMethods[:1], message: `setup failed: aTrust authentication method "auth/psw" is unavailable`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := newTestEnv(t)
			env.withDiscovery(testCase.methods, testCase.discErr)
			testCase.request.SOCKSListen = config.DefaultSOCKSListen
			_, err := Setup(context.Background(), env.deps, testCase.request)
			if err == nil || IsUsage(err) != testCase.usage || err.Error() != testCase.message {
				t.Fatalf("Setup() = %v (usage %t)", err, IsUsage(err))
			}
			if _, statErr := os.Stat(env.paths.Config); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("configuration written after failure: %v", statErr)
			}
		})
	}
}

func TestSetupEmptyPasswordWritesNothing(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.answer("\n", true)
	_, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", Server: "vpn.example.edu", Username: "student",
		SOCKSListen: config.DefaultSOCKSListen, PasswordSupplied: true,
	})
	if !errors.Is(err, credential.ErrEmptyCredential) || err.Error() != "setup failed: read password: credential is empty" {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(env.paths.Config); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("configuration written after failure: %v", statErr)
	}
}

func TestSaveRequiresReaderAndStoreForPasswordProfiles(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	configured := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if err := save(env.paths, configured, nil, func() ([]byte, error) { return []byte("x"), nil }); err == nil {
		t.Fatal("save accepted a password profile without a store")
	}
	if err := save(env.paths, configured, nil, nil); err == nil {
		t.Fatal("save accepted a password profile without a reader")
	}
	if err := save(env.paths, config.Config{}, nil, nil); err == nil {
		t.Fatal("save accepted an invalid configuration")
	}
}

func TestMigrateCopiesConfigAndImportsCredential(t *testing.T) {
	t.Parallel()
	legacyRoot := t.TempDir()
	legacy, err := config.LegacyPaths(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if err := config.Replace(legacy.Config, want); err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t)
	env.setSecret(legacy.Credential, "synthetic-password")

	result, err := Migrate(env.deps, legacyRoot)
	if err != nil || result != (MigrateResult{ConfigurationMigrated: true, CredentialMigrated: true}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got, err := config.Load(env.paths.Config); err != nil || got != want {
		t.Fatalf("configuration = %+v, err = %v", got, err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
	if secret, err := env.secret(legacy.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("legacy source was not preserved: %q, %v", secret, err)
	}
	result, err = Migrate(env.deps, legacyRoot)
	if err != nil || result.ConfigurationMigrated {
		t.Fatalf("second Migrate() = %+v, %v", result, err)
	}
}
