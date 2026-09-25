//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
	"github.com/soundadam/soundconnect/internal/speedtest"
)

// These tests cover the key flows the macOS app and users drive end to end:
// setup, first run, dry-run, aTrust connect, background start, logout, and
// the speed-test event stream.

func TestFlowSetupEasyConnectFromAppPipe(t *testing.T) {
	harness := newCLIHarness(t)
	harness.stdin("synthetic-password\n")
	result := harness.run("setup", "--backend", "easyconnect", "--server", "vpn.example.edu",
		"--username", "student", "--password-stdin").expect(t, 0)
	harness.golden("setup_easyconnect_password_stdin", result)

	got, err := config.Load(harness.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{Backend: backend.EasyConnect, Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}
	if got != want {
		t.Fatalf("configuration = %#v", got)
	}
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
}

func TestFlowSetupATrustPasswordFromAppPipe(t *testing.T) {
	harness := newCLIHarness(t)
	harness.stdin("synthetic-password\n")
	result := harness.run("setup", "--backend", "atrust", "--server", config.DefaultATrustServer,
		"--username", "student", "--password-stdin").expect(t, 0)
	harness.golden("setup_atrust_password_stdin", result)

	got, err := config.Load(harness.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendName() != backend.ATrust || got.AuthType != app.ATrustPasswordAuthType || got.LoginDomain != "ldap-domain" || got.Username != "student" {
		t.Fatalf("configuration = %#v", got)
	}
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("password = %q, err = %v", secret, err)
	}
}

func TestFlowSetupATrustOAuthStoresNoPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.stdin("")
	result := harness.run("setup", "--backend", "atrust", "--server", config.DefaultATrustServer).expect(t, 0)
	harness.golden("setup_atrust_oauth", result)
	got, err := config.Load(harness.paths.Config)
	if err != nil || got.AuthType != app.ATrustOAuthAuthType || got.LoginDomain != "oauth-domain" {
		t.Fatalf("configuration = %#v, err = %v", got, err)
	}
	if _, err := os.Lstat(harness.paths.Credential); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OAuth setup stored a password: %v", err)
	}
}

func TestFlowSetupPromptsNeedATerminalForThePassword(t *testing.T) {
	harness := newCLIHarness(t)
	// Gateway accepts the default, then the account is read; the hidden
	// password prompt refuses a non-terminal input.
	harness.stdin("\nstudent\n")
	result := harness.run("setup").expect(t, 1)
	harness.golden("setup_prompts_without_terminal", result)
	if _, err := os.Lstat(harness.paths.Config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup wrote configuration before the password was read: %v", err)
	}
}

func TestFlowSetupRejectsEmptyPipedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.stdin("\n")
	harness.golden("setup_empty_password_stdin", harness.run("setup", "--server", "vpn.example.edu",
		"--username", "student", "--password-stdin").expect(t, 1))
}

func TestFlowSetupKeepsSettingsItIsNotGiven(t *testing.T) {
	// Re-running setup changes only what its flags name; the listener and
	// upstream proxy survive a password or account change.
	harness := newCLIHarness(t)
	harness.writeConfig(config.Config{
		Server: "vpn.example.edu", Username: "old-student", SOCKSListen: "127.0.0.1:1090",
		UpstreamProxy: "socks5://127.0.0.1:7890",
	})
	harness.stdin("synthetic-password\n")
	harness.run("setup", "--server", "vpn.example.edu", "--username", "student", "--password-stdin").expect(t, 0)
	got, err := config.Load(harness.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamProxy != "socks5://127.0.0.1:7890" || got.SOCKSListen != "127.0.0.1:1090" || got.Username != "student" {
		t.Fatalf("configuration = %#v", got)
	}
}

func TestFlowFirstRunWithoutConfiguration(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("first_run_default_command", harness.run().expect(t, 1))
	harness.golden("first_run_connect", harness.run("connect").expect(t, 1))
	harness.golden("first_run_dry_run", harness.run("dry-run").expect(t, 1))
}

func TestFlowConnectWithoutSavedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	harness.golden("connect_without_password", harness.run("connect").expect(t, 1))
}

func TestFlowDryRunAgainstFakeGateway(t *testing.T) {
	harness := newCLIHarness(t)
	server := newNativeGatewayTestServer(t)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	harness.writeConfig(config.Config{
		Server: parsed.Host, Username: "fixture-account", SOCKSListen: config.DefaultSOCKSListen, TLSInsecure: true,
	})
	harness.writeSecret(harness.paths.Credential, "fixture-password")
	result := harness.run("dry-run").expect(t, 0)
	harness.golden("dry_run_accepted", result)
	for _, sensitive := range []string{"fixture-account", "fixture-password", "fedcba9876543210", nativeSSLContextFixture} {
		if strings.Contains(result.stdout+result.stderr, sensitive) {
			t.Fatalf("dry-run exposed %q", sensitive)
		}
	}
}

func TestFlowLogout(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeSecret(harness.paths.ATrustClientData, "synthetic-client-data")
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	helper := filepath.Join(t.TempDir(), "oauth-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n[ \"$1\" = --clear-data ]\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", helper)

	harness.golden("logout", harness.run("logout").expect(t, 0))
	if _, err := os.Lstat(harness.paths.ATrustClientData); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aTrust session remains: %v", err)
	}
	if secret, err := harness.readSecret(harness.paths.Credential); err != nil || secret != "synthetic-password" {
		t.Fatalf("logout touched the shared password: %q, %v", secret, err)
	}
	// Logging out twice is harmless.
	harness.run("logout").expect(t, 0)

	failing := filepath.Join(t.TempDir(), "failing-helper")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", failing)
	harness.golden("logout_helper_failure", harness.run("logout").expect(t, 1))
}

// connectATrust starts the connect command in the background and returns a
// function that waits for it to exit.
func (harness *cliHarness) connectATrust(ctx context.Context, arguments ...string) func() cliResult {
	harness.t.Helper()
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() {
		done <- runNativeConnectContext(ctx, arguments, &stdout, &stderr,
			func(nativeapp.SessionConfig) (nativeApplicationSession, error) {
				return nil, errors.New("aTrust must not use the EasyConnect session factory")
			}, nil)
	}()
	return func() cliResult {
		harness.t.Helper()
		select {
		case code := <-done:
			return cliResult{args: append([]string{"connect"}, arguments...), code: code, stdout: stdout.String(), stderr: stderr.String()}
		case <-time.After(15 * time.Second):
			harness.t.Fatal("connect did not exit")
			return cliResult{}
		}
	}
}

func (harness *cliHarness) writeATrustPasswordConfig() {
	harness.writeConfig(config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer, Username: "student",
		SOCKSListen: "127.0.0.1:0", AuthType: app.ATrustPasswordAuthType,
	})
}

func TestFlowATrustFreshLoginThenDisconnect(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.core.factors = []string{"password"}

	wait := harness.connectATrust(context.Background())
	harness.waitForRuntime("connected")
	status := harness.run("status", "--json").expect(t, 0)
	if !strings.Contains(status.stdout, `"profile":"atrust-tcp"`) || !strings.Contains(status.stdout, `"access_evidence":"available"`) {
		t.Fatalf("live aTrust status\n%s", status)
	}
	harness.run("disconnect").expect(t, 0)
	result := wait().expect(t, 0)

	if !strings.HasPrefix(result.stdout, "backend: atrust\nauthentication: accepted\nsocks: 127.0.0.1:") ||
		!strings.Contains(result.stdout, "access: available=true\nstate: connected\n") {
		t.Fatalf("connect output\n%s", result)
	}
	if strings.Contains(result.stdout+result.stderr, "synthetic-password") {
		t.Fatal("connect exposed the password")
	}
	if len(harness.core.passwords) != 1 || harness.core.passwords[0] != "synthetic-password" {
		t.Fatalf("prompted passwords = %q", harness.core.passwords)
	}
	if saved, err := harness.readSecret(harness.paths.ATrustClientData); err != nil || saved != "fresh-client-data" {
		t.Fatalf("saved client data = %q, err = %v", saved, err)
	}
}

func TestFlowATrustResumesSavedSessionWithoutPrompting(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.ATrustClientData, "saved-client-data")
	harness.core.resumable = "saved-client-data"
	harness.core.factors = []string{"password"}

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx)
	harness.waitForRuntime("connected")
	cancel()
	result := wait().expect(t, 0)
	if !strings.HasPrefix(result.stdout, "backend: atrust\nauthentication: resumed\n") {
		t.Fatalf("resume output\n%s", result)
	}
	if harness.core.logins != 0 || harness.core.resumes != 1 {
		t.Fatalf("logins = %d, resumes = %d", harness.core.logins, harness.core.resumes)
	}
}

func TestFlowATrustExpiredSessionFallsBackToLoginWithVerificationCode(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.writeSecret(harness.paths.ATrustClientData, "expired-client-data")
	harness.core.factors = []string{"password", "sms"}
	harness.stdin("123456\n")

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx, "--verification-code-stdin")
	harness.waitForRuntime("connected")
	cancel()
	result := wait().expect(t, 0)
	if !strings.Contains(result.stdout, "authentication: accepted\n") || !strings.Contains(result.stderr, "Verification code: ") {
		t.Fatalf("SMS login output\n%s", result)
	}
	if len(harness.core.codes) != 1 || harness.core.codes[0] != "123456" {
		t.Fatalf("verification codes = %q", harness.core.codes)
	}
	if harness.core.resumes != 1 || harness.core.logins != 1 {
		t.Fatalf("logins = %d, resumes = %d", harness.core.logins, harness.core.resumes)
	}
}

func TestFlowATrustVerificationCodeRequiresTerminalWithoutStdinFlag(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	harness.core.factors = []string{"password", "sms"}
	harness.stdin("123456\n")

	result := harness.connectATrust(context.Background())().expect(t, 1)
	harness.golden("atrust_verification_code_without_terminal", result)
}

func TestFlowATrustCancelledLoginExitsCleanly(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.core.blockOnAuth = true

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		harness.core.mu.Lock()
		logins := harness.core.logins
		harness.core.mu.Unlock()
		if logins == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("login never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	result := wait().expect(t, 0)
	if result.stdout != "" || result.stderr != "" {
		t.Fatalf("cancelled login output\n%s", result)
	}
}

func TestFlowATrustWithoutSavedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.core.factors = []string{"password"}
	harness.golden("atrust_without_password", harness.connectATrust(context.Background())().expect(t, 1))
}

func TestFlowATrustRejectsBackgroundRuntime(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustPasswordConfig()
	harness.golden("atrust_background_rejected", harness.run("connect", "--background").expect(t, 2))
}

func TestFlowBackgroundChildStartupFailureIsReported(t *testing.T) {
	harness := newCLIHarness(t)
	t.Setenv(cliReexecEnv, "1")
	if err := os.MkdirAll(harness.root, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(harness.root, "runtime.log")
	// The child accepts only the EasyConnect runtime profile, so this valid
	// handoff with another profile makes it exit before signalling readiness.
	_, err := startProductionNativeBackground(nativeapp.SessionConfig{
		Settings:           config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen},
		NativeGatewayToken: make(sessiontoken.NativeGatewayToken, sessiontoken.NativeGatewayTokenSize),
		NativeProfile:      runtime.ProfileATrustTCP,
	}, logPath)
	if err == nil || err.Error() != "background native runtime did not initialize" {
		t.Fatalf("background start error = %v", err)
	}
	log, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(log) != "background runtime: private handoff is invalid\n" {
		t.Fatalf("background log = %q", log)
	}
	info, statErr := os.Stat(logPath)
	if statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("background log mode = %v, err = %v", info.Mode(), statErr)
	}
}

func TestFlowBackgroundRejectsInvalidHandoffBeforeStartingChildWork(t *testing.T) {
	harness := newCLIHarness(t)
	t.Setenv(cliReexecEnv, "1")
	if err := os.MkdirAll(harness.root, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := startProductionNativeBackground(nativeapp.SessionConfig{
		NativeGatewayToken: sessiontoken.NativeGatewayToken("short"),
		NativeProfile:      runtime.ProfileCommunityUTLSCompat,
	}, filepath.Join(harness.root, "runtime.log"))
	if err == nil || err.Error() != "transfer private background handoff" {
		t.Fatalf("background start error = %v", err)
	}
}

const speedtestFixtureHelper = `#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf 'librespeed-cli v1.0.13-campus.1 (built on test)\n'
  exit 0
fi
cat >/dev/null
printf '%s\n' '{"type":"progress","test":"download","elapsed_ms":1000,"bytes":6250000,"mbps":50}' >&2
printf '%s\n' '{"type":"progress","test":"upload","elapsed_ms":1000,"bytes":1250000,"mbps":10}' >&2
printf '%s\n' '[{"server":{"name":"NJU Campus IPv4","url":"http://speed.nju.edu.cn"},"ping":6,"jitter":1,"upload":10,"download":50}]'
`

// useSpeedtestFixtures serves the measurement component from a local TLS
// server and makes the reachability probe succeed without a network.
func (harness *cliHarness) useSpeedtestFixtures() {
	harness.t.Helper()
	helper := []byte(speedtestFixtureHelper)
	digest := sha256.Sum256(helper)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(helper)
	}))
	previousAsset := speedtestAsset
	previousExternal := speedtestExternalPath
	previousHTTP := speedtestHTTPClient
	previousTerminal := speedtestIsTerminal
	previousStdin := speedtestStdin
	previousProbe := speedtestProbe
	speedtestAsset = func() speedtest.ComponentAsset {
		return speedtest.ComponentAsset{
			Version: "test", HelperVersion: speedtest.HelperVersion,
			OS: goruntime.GOOS, Architecture: goruntime.GOARCH,
			URL: server.URL, Size: int64(len(helper)), SHA256: hex.EncodeToString(digest[:]),
		}
	}
	speedtestExternalPath = func() string { return "" }
	speedtestHTTPClient = func() *http.Client { return server.Client() }
	speedtestIsTerminal = func() bool { return false }
	speedtestStdin = strings.NewReader("")
	speedtestProbe = func(context.Context, speedtest.Route, string) error { return nil }
	harness.t.Cleanup(func() {
		server.Close()
		speedtestAsset = previousAsset
		speedtestExternalPath = previousExternal
		speedtestHTTPClient = previousHTTP
		speedtestIsTerminal = previousTerminal
		speedtestStdin = previousStdin
		speedtestProbe = previousProbe
	})
}

// TestFlowSpeedtestAppInvocations runs every speed-test command line the
// macOS app issues, in the order it issues them, and freezes their output.
func TestFlowSpeedtestAppInvocations(t *testing.T) {
	harness := newCLIHarness(t)
	harness.useSpeedtestFixtures()
	// The component is per-architecture; normalize it so the fixtures are
	// identical on every CI runner.
	architecture := func(value string) string {
		value = strings.ReplaceAll(value, `"architecture":"`+goruntime.GOARCH+`"`, `"architecture":"$ARCH"`)
		return strings.ReplaceAll(value, "/"+goruntime.GOARCH+"/", "/$ARCH/")
	}
	normalizePath := func(value string) string {
		return architecture(strings.ReplaceAll(value, harness.root, "$CONFIG_DIR"))
	}

	missing := harness.run("speedtest", "component", "status", "--json").expect(t, 1)
	harness.golden("speedtest_component_status_missing_json", missing, architecture)
	contractFixture(t, "speedtest_component_status_missing.json", normalizePath(missing.stdout))

	harness.golden("speedtest_campus_json_without_component",
		harness.run("speedtest", "campus", "--route", "auto", "--json-events").expect(t, 1))
	harness.golden("speedtest_component_install_without_yes",
		harness.run("speedtest", "component", "install", "--json-events").expect(t, 1))

	install := harness.run("speedtest", "component", "install", "--yes", "--json-events").expect(t, 0)
	harness.golden("speedtest_component_install_events", install)
	contractFixture(t, "speedtest_component_install_events.ndjson", install.stdout)

	installed := harness.run("speedtest", "component", "status", "--json").expect(t, 0)
	harness.golden("speedtest_component_status_installed_json", installed, architecture)
	contractFixture(t, "speedtest_component_status_installed.json", normalizePath(installed.stdout))

	probe := harness.run("speedtest", "probe", "--route", "auto", "--json").expect(t, 0)
	latency := stableJSONNumber("latency_ms")
	harness.golden("speedtest_probe_json", probe, latency)
	contractFixture(t, "speedtest_probe.json", latency(probe.stdout))

	harness.golden("speedtest_last_missing_json", harness.run("speedtest", "last", "--json").expect(t, 1))

	campus := harness.run("speedtest", "campus", "--route", "auto", "--json-events").expect(t, 0)
	harness.golden("speedtest_campus_events", campus, stableTimes)
	contractFixture(t, "speedtest_campus_events.ndjson", stableTimes(campus.stdout))

	last := harness.run("speedtest", "last", "--json").expect(t, 0)
	harness.golden("speedtest_last_json", last, stableTimes)
	contractFixture(t, "speedtest_last.json", stableTimes(last.stdout))
	harness.golden("speedtest_last_text", harness.run("speedtest", "last").expect(t, 0))
}

func TestFlowSpeedtestSoundConnectRouteRequiresRuntime(t *testing.T) {
	harness := newCLIHarness(t)
	harness.useSpeedtestFixtures()
	harness.golden("speedtest_probe_soundconnect_without_runtime",
		harness.run("speedtest", "probe", "--route", "soundconnect", "--json").expect(t, 1))
}

func TestFlowMigrate(t *testing.T) {
	harness := newCLIHarness(t)
	legacyRoot := t.TempDir()
	legacy, err := config.LegacyPaths(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Replace(legacy.Config, config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen}); err != nil {
		t.Fatal(err)
	}
	harness.writeSecret(legacy.Credential, "synthetic-password")
	legacyPath := func(value string) string { return strings.ReplaceAll(value, legacyRoot, "$LEGACY_ROOT") }
	harness.golden("migrate_first", harness.run("migrate", "--from", legacyRoot).expect(t, 0), legacyPath)
	harness.golden("migrate_again", harness.run("migrate", "--from", legacyRoot).expect(t, 0), legacyPath)
}

// lockedBuffer is a bytes.Buffer safe for a command goroutine writing while
// the test reads after it exits.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type easyConnectGatewayFixture struct {
	requireSMS     bool
	rejectPassword bool
	acceptedCode   string
}

// newEasyConnectGateway serves the EasyConnect login endpoints with an
// optional SMS stage or a password rejection, then the bootstrap endpoints of
// newNativeGatewayTestServer.
func newEasyConnectGateway(t *testing.T, fixture easyConnectGatewayFixture) *url.URL {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/por/login_auth.csp":
			fmt.Fprintf(writer, "<Auth><ErrorCode>1</ErrorCode><TwfID>fedcba9876543210</TwfID><RSA_ENCRYPT_KEY>%s</RSA_ENCRYPT_KEY><RSA_ENCRYPT_EXP>65537</RSA_ENCRYPT_EXP><CSRF_RAND_CODE>fixture-nonce</CSRF_RAND_CODE></Auth>", privateKey.N.Text(16))
		case "/por/login_psw.csp":
			switch {
			case fixture.rejectPassword:
				fmt.Fprint(writer, "<Auth><ErrorCode>0</ErrorCode></Auth>")
			case fixture.requireSMS:
				fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode><NextService>auth/sms</NextService></Auth>")
			default:
				fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode></Auth>")
			}
		case "/por/login_sms.csp":
			fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode></Auth>")
		case "/por/login_sms1.csp":
			if request.FormValue("svpn_inputsms") == fixture.acceptedCode {
				fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode></Auth>")
			} else {
				fmt.Fprint(writer, "<Auth><ErrorCode>0</ErrorCode></Auth>")
			}
		case "/por/conf.csp":
			fmt.Fprintf(writer, `<Conf><Other sslctx="%s"/></Conf>`, nativeSSLContextFixture)
		case "/por/rclist.csp":
			fmt.Fprint(writer, `<Resource><Rcs><Rc type="2"/></Rcs></Resource>`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func (harness *cliHarness) writeEasyConnectState(gateway *url.URL, password string) {
	harness.t.Helper()
	harness.writeConfig(config.Config{
		Server: gateway.Host, Username: "fixture-account", SOCKSListen: config.DefaultSOCKSListen,
		TLSInsecure: true, NativeTLSInsecure: true,
	})
	harness.writeSecret(harness.paths.Credential, password)
}

// connectEasyConnectBackground runs the exact command line the macOS app uses
// for EasyConnect, with a fake detached-runtime starter.
func (harness *cliHarness) connectEasyConnectBackground() cliResult {
	harness.t.Helper()
	arguments := []string{"--background", "--verification-code-stdin"}
	var stdout, stderr bytes.Buffer
	code := runNativeConnectContext(context.Background(), arguments, &stdout, &stderr,
		func(nativeapp.SessionConfig) (nativeApplicationSession, error) {
			harness.t.Fatal("background connect reached the foreground session factory")
			return nil, nil
		},
		func(sessionConfig nativeapp.SessionConfig, logPath string) (int, error) {
			if sessionConfig.Observer != nil || !sessionConfig.Plan.BoundaryReady {
				harness.t.Fatal("background session config is invalid")
			}
			return 4242, nil
		})
	return cliResult{args: append([]string{"connect"}, arguments...), code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestFlowEasyConnectBackgroundWithVerificationCodeFromAppPipe(t *testing.T) {
	harness := newCLIHarness(t)
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{requireSMS: true, acceptedCode: "123456"})
	harness.writeEasyConnectState(gateway, "fixture-password")
	harness.stdin("123456\n")
	harness.golden("easyconnect_background_verification_code", harness.connectEasyConnectBackground().expect(t, 0))
}

func TestFlowEasyConnectRejectedVerificationCode(t *testing.T) {
	harness := newCLIHarness(t)
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{requireSMS: true, acceptedCode: "123456"})
	harness.writeEasyConnectState(gateway, "fixture-password")
	harness.stdin("000000\n")
	harness.golden("easyconnect_rejected_verification_code", harness.connectEasyConnectBackground().expect(t, 1))
}

func TestFlowEasyConnectRejectedPassword(t *testing.T) {
	harness := newCLIHarness(t)
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{rejectPassword: true})
	harness.writeEasyConnectState(gateway, "wrong-password")
	harness.golden("easyconnect_rejected_password", harness.connectEasyConnectBackground().expect(t, 1))
	harness.golden("dry_run_rejected_password", harness.run("dry-run").expect(t, 1))
}

func TestFlowEasyConnectVerificationCodeRequiresTerminalWithoutStdinFlag(t *testing.T) {
	harness := newCLIHarness(t)
	gateway := newEasyConnectGateway(t, easyConnectGatewayFixture{requireSMS: true, acceptedCode: "123456"})
	harness.writeEasyConnectState(gateway, "fixture-password")
	harness.stdin("123456\n")
	harness.golden("dry_run_verification_code_without_terminal", harness.run("dry-run").expect(t, 1))
}

func (harness *cliHarness) writeATrustOAuthConfig() {
	harness.writeConfig(config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer,
		SOCKSListen: "127.0.0.1:0", AuthType: app.ATrustOAuthAuthType,
	})
}

func TestFlowATrustOAuthThroughBundledHelper(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustOAuthConfig()
	harness.core.factors = []string{"oauth"}
	arguments := filepath.Join(t.TempDir(), "helper-arguments")
	helper := filepath.Join(t.TempDir(), "oauth-helper")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + arguments + "'\nprintf 'fixture-oauth-code\\n'\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", helper)

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx)
	harness.waitForRuntime("connected")
	cancel()
	result := wait().expect(t, 0)
	if len(harness.core.oauthCodes) != 1 || harness.core.oauthCodes[0] != "fixture-oauth-code" {
		t.Fatalf("OAuth codes = %q\n%s", harness.core.oauthCodes, result)
	}
	passed, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if string(passed) != "--login-url\nhttps://vpn.nju.edu.cn/portal/oauth-login\n--gateway-host\nvpn.nju.edu.cn\n--gateway-port\n443\n" {
		t.Fatalf("helper arguments = %q", passed)
	}
	if strings.Contains(result.stdout+result.stderr, "fixture-oauth-code") {
		t.Fatal("connect exposed the OAuth code")
	}
}

func TestFlowATrustOAuthHelperClosedWindow(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustOAuthConfig()
	harness.core.factors = []string{"oauth"}
	helper := filepath.Join(t.TempDir(), "oauth-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", helper)
	harness.golden("atrust_oauth_window_closed", harness.connectATrust(context.Background())().expect(t, 1))
}

func TestFlowATrustOAuthPastedCallbackWithoutHelper(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustOAuthConfig()
	harness.core.factors = []string{"oauth"}
	harness.stdin("https://vpn.nju.edu.cn/passport/v1/auth/httpsOauth2?code=pasted-code\n")

	ctx, cancel := context.WithCancel(context.Background())
	wait := harness.connectATrust(ctx)
	harness.waitForRuntime("connected")
	cancel()
	result := wait().expect(t, 0)
	if len(harness.core.oauthCodes) != 1 || harness.core.oauthCodes[0] != "pasted-code" {
		t.Fatalf("OAuth codes = %q\n%s", harness.core.oauthCodes, result)
	}
	want := "Visit https://vpn.nju.edu.cn/portal/oauth-login to sign in.\n" +
		"Paste the resulting /passport/v1/auth/httpsOauth2 callback URL here; it stays local.\n" +
		"Callback URL: "
	if result.stderr != want {
		t.Fatalf("OAuth prompt = %q", result.stderr)
	}
}

func TestFlowATrustOAuthRejectsForeignCallback(t *testing.T) {
	harness := newCLIHarness(t)
	harness.writeATrustOAuthConfig()
	harness.core.factors = []string{"oauth"}
	harness.stdin("https://attacker.example/passport/v1/auth/httpsOauth2?code=stolen\n")
	harness.golden("atrust_oauth_foreign_callback", harness.connectATrust(context.Background())().expect(t, 1))
}
