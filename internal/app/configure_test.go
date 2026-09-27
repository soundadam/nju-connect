//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
)

func TestConfigureSwitchesToATrustWithoutTouchingSecrets(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.writeConfig(config.Config{
		Backend: backend.EasyConnect, Server: config.DefaultServer, Username: "123456789",
		SOCKSListen: config.DefaultSOCKSListen, UpstreamProxy: "socks5://127.0.0.1:9050",
	})
	env.setSecret(env.paths.Credential, "synthetic-password")

	result, err := Configure(env.deps, ConfigureRequest{Backend: "atrust"})
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer, Username: "123456789",
		SOCKSListen: config.DefaultSOCKSListen, UpstreamProxy: "socks5://127.0.0.1:9050",
		AuthType: ATrustPasswordAuthType,
	}
	if result.Config != want || result.Path != env.paths.Config {
		t.Fatalf("result = %+v", result)
	}
	if got, err := config.Load(env.paths.Config); err != nil || got != want {
		t.Fatalf("saved = %+v, err = %v", got, err)
	}
	if secret, err := env.secret(env.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
}

func TestConfigureSwitchesBackToEasyConnectAndClearsATrustFields(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.writeConfig(config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer, Username: "123456789",
		SOCKSListen: "127.0.0.1:19081", AuthType: ATrustPasswordAuthType, LoginDomain: "openldap13924",
	})
	result, err := Configure(env.deps, ConfigureRequest{Backend: "easyconnect"})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Config
	if got.BackendName() != backend.EasyConnect || got.Server != config.DefaultServer ||
		got.SOCKSListen != "127.0.0.1:19081" || got.AuthType != "" || got.LoginDomain != "" {
		t.Fatalf("configured = %+v", got)
	}
}

func TestConfigureKeepsSettingsWithinTheSameBackend(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	env.writeConfig(config.Config{
		Backend: backend.ATrust, Server: "vpn.example.edu", Username: "student",
		SOCKSListen: config.DefaultSOCKSListen, AuthType: ATrustOAuthAuthType, LoginDomain: "tenant",
	})
	result, err := Configure(env.deps, ConfigureRequest{UpstreamProxy: "socks5://127.0.0.1:9050"})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Config
	if got.Server != "vpn.example.edu" || got.AuthType != ATrustOAuthAuthType || got.LoginDomain != "tenant" ||
		got.UpstreamProxy != "socks5://127.0.0.1:9050" {
		t.Fatalf("configured = %+v", got)
	}
}

func TestConfigureUsageErrors(t *testing.T) {
	t.Parallel()
	for name, request := range map[string]ConfigureRequest{
		"unknown backend":     {Backend: "openvpn"},
		"unsupported auth":    {Backend: "atrust", Username: "student", AuthType: "auth/cas"},
		"atrust without user": {Backend: "atrust"},
	} {
		t.Run(name, func(t *testing.T) {
			env := newTestEnv(t)
			if _, err := Configure(env.deps, request); !IsUsage(err) {
				t.Fatalf("Configure() = %v", err)
			}
			if _, err := os.Stat(env.paths.Config); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("configuration written after a usage error: %v", err)
			}
		})
	}
}

func TestConfigureRefusesLiveRuntime(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	server := env.serveRuntime()
	defer server.Close()
	_, err := Configure(env.deps, ConfigureRequest{Backend: "atrust", Username: "student"})
	if !errors.Is(err, runtimecontrol.ErrAlreadyActive) || IsUsage(err) {
		t.Fatalf("Configure() = %v", err)
	}
}

func TestSetupRefusesLiveRuntime(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	defer env.serveRuntime().Close()
	_, err := Setup(context.Background(), env.deps, SetupRequest{
		Backend: "easyconnect", Server: "vpn.example.edu", Username: "student", PasswordSupplied: true,
	})
	if err == nil || err.Error() != "setup profile: another native runtime is already active" {
		t.Fatalf("err = %v", err)
	}
}

func (env *testEnv) serveRuntime() *runtimecontrol.Server {
	env.t.Helper()
	server, err := runtimecontrol.Serve(runtimecontrol.Path(env.paths.Root), func() runtimecontrol.Snapshot {
		return runtimecontrol.Snapshot{
			SchemaVersion: runtimecontrol.SchemaVersion, Running: true, State: "connected",
			Profile: runtime.ProfileCommunityUTLSCompat, AccessEvidence: "available",
		}
	})
	if err != nil {
		env.t.Fatal(err)
	}
	return server
}
