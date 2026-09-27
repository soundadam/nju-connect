package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/backend/atrust"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
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
		Backend: "easyconnect", PasswordSupplied: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != env.paths.Config || result.Backend != backend.EasyConnect || result.Credential != CredentialSystemStore {
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
		PasswordSupplied: true,
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
	env.deps.PasswordStore = func(credential.Location) (credential.Store, error) {
		t.Fatal("OAuth setup opened the password store")
		return nil, nil
	}
	result, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn",
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
		Backend: "atrust", Server: "vpn.nju.edu.cn", PasswordSupplied: true,
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
		PasswordSupplied: true,
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
	if err := save(env.paths, config.Config{}, nil, nil); err == nil {
		t.Fatal("save accepted an invalid configuration")
	}
}

func TestSetupKeepsSavedSettingsThatTheRequestLeavesOut(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	saved := config.Config{
		Backend: backend.EasyConnect, Server: "vpn.example.edu", Username: "student",
		SOCKSListen: "127.0.0.1:1090", UpstreamProxy: "socks5://127.0.0.1:7890",
		TLSInsecure: true, NativeTLSInsecure: true, CredentialStore: credential.BackendFile,
	}
	env.writeConfig(saved)
	env.answer("new-password\n", true)
	// The macOS app re-runs setup this way to replace the password.
	if _, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", Server: "vpn.example.edu", Username: "student", PasswordSupplied: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := config.Load(env.paths.Config); err != nil || got != saved {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "new-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}

	listen, proxy, insecure := "127.0.0.1:1100", "", false
	env.answer("newer-password\n", true)
	if _, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", Server: "vpn.example.edu", Username: "student", PasswordSupplied: true,
		SOCKSListen: &listen, UpstreamProxy: &proxy, TLSInsecure: &insecure,
	}); err != nil {
		t.Fatal(err)
	}
	want := saved
	want.SOCKSListen, want.UpstreamProxy, want.TLSInsecure = listen, "", false
	if got, err := config.Load(env.paths.Config); err != nil || got != want {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
}

func TestSetupStoresThePasswordBeforeTheConfiguration(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	saved := config.Config{Backend: backend.EasyConnect, Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	env.writeConfig(saved)
	failure := errors.New("keyring locked")
	env.deps.PasswordStore = func(credential.Location) (credential.Store, error) { return failingStore{err: failure}, nil }
	env.answer("new-password\n", true)
	_, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", Server: "vpn.other.edu", Username: "other", PasswordSupplied: true,
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
	if got, err := config.Load(env.paths.Config); err != nil || got != saved {
		t.Fatalf("configuration changed after a failed password write: %+v, %v", got, err)
	}
}

type failingStore struct{ err error }

func (store failingStore) Inspect() error          { return os.ErrNotExist }
func (store failingStore) Get() ([]byte, error)    { return nil, os.ErrNotExist }
func (store failingStore) Set(secret []byte) error { return store.err }

func TestSetupForgetsTheATrustSessionWhenTheAccountChanges(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.withDiscovery(njuMethods, nil)
	env.writeConfig(config.Config{
		Backend: backend.ATrust, Server: "vpn.nju.edu.cn", Username: "student", SOCKSListen: config.DefaultSOCKSListen,
		AuthType: ATrustPasswordAuthType, LoginDomain: "ldap-domain",
	})
	env.setSecret(env.paths.ATrustClientData, "client-data")

	env.answer("synthetic-password\n", true)
	result, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn", Username: "student", PasswordSupplied: true,
	})
	if err != nil || result.SessionCleared {
		t.Fatalf("same account: result=%+v err=%v", result, err)
	}
	if _, err := env.secret(env.paths.ATrustClientData); err != nil {
		t.Fatalf("session forgotten for the same account: %v", err)
	}

	env.answer("synthetic-password\n", true)
	result, err = Setup(context.Background(), env.deps, SetupRequest{
		Backend: "atrust", Server: "vpn.nju.edu.cn", Username: "classmate", PasswordSupplied: true,
	})
	if err != nil || !result.SessionCleared {
		t.Fatalf("new account: result=%+v err=%v", result, err)
	}
	if _, err := env.secret(env.paths.ATrustClientData); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session kept for another account: %v", err)
	}
}

func TestGuidedSetupAsksForEveryChoice(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.withDiscovery(njuMethods, nil)
	// Backend 2 (aTrust), default gateway, default sign-in method (OAuth).
	output := env.answer("2\n\n\n", false)
	result, err := Setup(context.Background(), env.deps, SetupRequest{Guided: true})
	if err != nil || result.Credential != CredentialBrowserOAuth {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	wantPrompts := "  1) EasyConnect\n  2) aTrust\nProtocol backend [easyconnect]: " +
		"Gateway [vpn.nju.edu.cn]: " +
		"  1) Browser sign-in (OAuth) — Unified identity\n  2) Account and password — Account password\n" +
		"Sign-in method [auth/httpsOauth2]: "
	if output.String() != wantPrompts {
		t.Fatalf("prompts = %q", output.String())
	}

	// Re-running starts from the saved choices; switching to the password
	// method asks for the account, and keeps nothing it cannot reuse.
	output = env.answer("\n\n2\nstudent\nsynthetic-password\n", true)
	if result, err = Setup(context.Background(), env.deps, SetupRequest{Guided: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Protocol backend [atrust]: ") || !strings.HasSuffix(output.String(), "Account: ") {
		t.Fatalf("prompts = %q", output.String())
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}

	// With the password saved for the same account, it can be kept.
	output = env.answer("\n\n\n\ny\n", false)
	if _, err = Setup(context.Background(), env.deps, SetupRequest{Guided: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Sign-in method [auth/psw]: Account [student]: Keep the saved VPN password? [Y/n]: ") {
		t.Fatalf("prompts = %q", output.String())
	}
}
