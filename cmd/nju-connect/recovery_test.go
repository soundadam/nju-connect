//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
)

// connectBackgroundAnswering runs a background EasyConnect connect as a
// person at a terminal who types script.
func (harness *cliHarness) connectBackgroundAnswering(script string) cliResult {
	harness.t.Helper()
	deps := answeredBy(harness.t, harness.deps, script)
	deps.StartBackground = func(nativeapp.SessionConfig, string) (int, error) { return 4242, nil }
	var stdout, stderr bytes.Buffer
	code := runConnectContext(context.Background(), deps, []string{"--background"}, &stdout, &stderr)
	return cliResult{args: []string{"connect", "--background"}, code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestRejectedPasswordIsReenteredAndSaved(t *testing.T) {
	harness := newCLIHarness(t)
	var usernames []string
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{rejectPasswords: 1, usernames: &usernames})
	harness.writeEasyConnectState(gateway, "wrong-password")

	result := harness.connectBackgroundAnswering("1\nright-password\n").expect(t, 0)
	if result.stdout != "authentication: accepted\nbackground: pid=4242 log="+harness.root+"/runtime.log\n" {
		t.Fatalf("connect output\n%s", result)
	}
	if result.stderr != "gateway rejected the username or password (gateway code 0)\n" {
		t.Fatalf("connect diagnostics\n%s", result)
	}
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "right-password" {
		t.Fatalf("saved password = %q, %v", secret, err)
	}
	if len(usernames) != 2 {
		t.Fatalf("password logins = %q", usernames)
	}
}

func TestRejectedAccountIsChangedAndSaved(t *testing.T) {
	harness := newCLIHarness(t)
	var usernames []string
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{rejectPasswords: 1, usernames: &usernames})
	harness.writeEasyConnectState(gateway, "wrong-password")
	harness.writeSecret(harness.paths.ATrustClientData, "old-account-session")

	harness.connectBackgroundAnswering("2\nnew-account\nright-password\n").expect(t, 0)
	if len(usernames) != 2 || usernames[0] != "fixture-account" || usernames[1] != "new-account" {
		t.Fatalf("password logins = %q", usernames)
	}
	saved, err := config.Load(harness.paths.Config)
	if err != nil || saved.Username != "new-account" || !saved.TLSInsecure {
		t.Fatalf("saved configuration = %+v, %v", saved, err)
	}
	if _, err := harness.readSecret(harness.paths.ATrustClientData); err == nil {
		t.Fatal("the old account's aTrust session was kept")
	}
}

func TestStoppingAfterARejectionKeepsTheSavedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{rejectPasswords: 1})
	harness.writeEasyConnectState(gateway, "wrong-password")

	harness.connectBackgroundAnswering("3\n").expect(t, 0)
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "wrong-password" {
		t.Fatalf("saved password = %q, %v", secret, err)
	}
}

func TestRejectedPasswordStopsAfterThreeAttempts(t *testing.T) {
	harness := newCLIHarness(t)
	var usernames []string
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{rejectPasswords: 5, usernames: &usernames})
	harness.writeEasyConnectState(gateway, "wrong-password")

	result := harness.connectBackgroundAnswering("1\nsecond\n1\nthird\n").expect(t, 1)
	if len(usernames) != 3 {
		t.Fatalf("password logins = %q", usernames)
	}
	harness.golden("easyconnect_rejected_password_three_times", result)
}

func TestFlowATrustRejectedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.Credential, "wrong-password")
	harness.core.factors = []string{"password"}
	harness.core.rejectPasswords = 1
	rejected := harness.connectATrust(context.Background())().expect(t, 1)
	harness.golden("atrust_rejected_password", rejected)
	contractFixture(t, "connect_credential_rejected_atrust.stderr", rejected.stderr)
}

func TestATrustRejectedPasswordIsReentered(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.Credential, "wrong-password")
	harness.core.factors = []string{"password"}
	harness.core.rejectPasswords = 1
	harness.deps = answeredBy(t, harness.deps, "1\nright-password\n")

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx)
	harness.waitForRuntime("connected")
	cancel()
	result := wait().expect(t, 0)
	if len(harness.core.passwords) != 2 || harness.core.passwords[1] != "right-password" {
		t.Fatalf("aTrust passwords = %q\n%s", harness.core.passwords, result)
	}
}
