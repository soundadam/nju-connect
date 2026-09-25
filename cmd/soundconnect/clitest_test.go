//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
)

var updateGolden = flag.Bool("update", false, "rewrite CLI golden files and shared JSON contract fixtures")

// cliHarness runs the real command dispatcher against an isolated
// SOUNDCONNECT_CONFIG_DIR. Secret stores are owner-only files and the aTrust
// protocol core is a fake, so no test touches the user's Keychain, the
// network, or a real gateway.
type cliHarness struct {
	t     *testing.T
	root  string
	paths config.Paths
	core  *harnessATrustCore
}

func newCLIHarness(t *testing.T) *cliHarness {
	t.Helper()
	root := filepath.Join(t.TempDir(), "soundconnect")
	t.Setenv("SOUNDCONNECT_CONFIG_DIR", root)
	t.Setenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER", "")
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.Root != root {
		t.Fatalf("SOUNDCONNECT_CONFIG_DIR resolved to %q", paths.Root)
	}
	core := newHarnessATrustCore()

	previousPassword := newSystemCredentialStore
	previousClientData := newATrustClientDataStore
	previousCore := newATrustCore
	previousStdin := os.Stdin
	newSystemCredentialStore = func(location credential.Location) (credential.Store, error) {
		return credential.NewFileStore(location.File, true)
	}
	newATrustClientDataStore = func(location credential.Location) (credential.Store, error) {
		return credential.NewFileStore(location.File, true)
	}
	newATrustCore = func() atrustbackend.Core { return core }
	t.Cleanup(func() {
		newSystemCredentialStore = previousPassword
		newATrustClientDataStore = previousClientData
		newATrustCore = previousCore
		os.Stdin = previousStdin
	})
	return &cliHarness{t: t, root: root, paths: paths, core: core}
}

// stdin replaces the process standard input with a non-terminal file holding
// content, which is how the macOS app feeds --password-stdin and
// --verification-code-stdin.
func (harness *cliHarness) stdin(content string) {
	harness.t.Helper()
	path := filepath.Join(harness.t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		harness.t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		harness.t.Fatal(err)
	}
	harness.t.Cleanup(func() { _ = file.Close() })
	os.Stdin = file
}

func (harness *cliHarness) writeConfig(configured config.Config) {
	harness.t.Helper()
	if err := config.Replace(harness.paths.Config, configured); err != nil {
		harness.t.Fatal(err)
	}
}

func (harness *cliHarness) writeSecret(path string, secret string) {
	harness.t.Helper()
	store, err := credential.NewFileStore(path, true)
	if err != nil {
		harness.t.Fatal(err)
	}
	if err := store.Set([]byte(secret)); err != nil {
		harness.t.Fatal(err)
	}
}

func (harness *cliHarness) readSecret(path string) (string, error) {
	harness.t.Helper()
	store, err := credential.NewFileStore(path, true)
	if err != nil {
		harness.t.Fatal(err)
	}
	secret, err := store.Get()
	return string(secret), err
}

// serveStatus publishes a fixed runtime snapshot on the private control
// socket the real runtime would own, and records disconnect requests.
func (harness *cliHarness) serveStatus(snapshot runtimecontrol.Snapshot) *fakeRuntimeControl {
	harness.t.Helper()
	server, err := runtimecontrol.Serve(runtimecontrol.Path(harness.root), func() runtimecontrol.Snapshot { return snapshot })
	if err != nil {
		harness.t.Fatal(err)
	}
	control := &fakeRuntimeControl{server: server}
	server.SetStop(control.stop)
	harness.t.Cleanup(func() { _ = server.Close() })
	return control
}

type fakeRuntimeControl struct {
	server *runtimecontrol.Server
	mu     sync.Mutex
	stops  int
}

func (control *fakeRuntimeControl) stop() {
	control.mu.Lock()
	control.stops++
	control.mu.Unlock()
}

func (control *fakeRuntimeControl) stopCount() int {
	control.mu.Lock()
	defer control.mu.Unlock()
	return control.stops
}

// fixedRunningSnapshot is a deterministic, fully populated live snapshot.
func fixedRunningSnapshot(profile runtime.ProtocolProfileID) runtimecontrol.Snapshot {
	startedAt := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	return runtimecontrol.Snapshot{
		SchemaVersion:  runtimecontrol.SchemaVersion,
		Running:        true,
		State:          "connected",
		StartedAt:      &startedAt,
		Profile:        profile,
		SOCKSListen:    "127.0.0.1:1080",
		AccessEvidence: "available",
		Traffic: &runtimecontrol.Traffic{
			SessionStartedAt:   &startedAt,
			SampledAtUnixMilli: startedAt.Add(90 * time.Second).UnixMilli(),
			UploadBytes:        12_345,
			DownloadBytes:      678_901,
			ActiveConnections:  2,
			TotalConnections:   17,
		},
	}
}

type cliResult struct {
	args   []string
	code   int
	stdout string
	stderr string
}

func (harness *cliHarness) run(arguments ...string) cliResult {
	harness.t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(arguments, &stdout, &stderr)
	return cliResult{args: arguments, code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (result cliResult) String() string {
	return fmt.Sprintf("soundconnect %s: exit=%d\nstdout:\n%s\nstderr:\n%s",
		strings.Join(result.args, " "), result.code, result.stdout, result.stderr)
}

// expect fails unless the command exited with code.
func (result cliResult) expect(t *testing.T, code int) cliResult {
	t.Helper()
	if result.code != code {
		t.Fatalf("want exit %d\n%s", code, result)
	}
	return result
}

// golden compares the whole observable result (arguments, exit code, stdout,
// and stderr) with testdata/cli/<name>.golden. Run the package tests with
// -update to rewrite the files after an intended change and review the diff.
func (harness *cliHarness) golden(name string, result cliResult, normalizers ...func(string) string) {
	harness.t.Helper()
	normalize := func(value string) string {
		value = strings.ReplaceAll(value, harness.root, "$CONFIG_DIR")
		for _, normalizer := range normalizers {
			value = normalizer(value)
		}
		return value
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s\n", normalize(strings.TrimSpace("$ soundconnect "+strings.Join(result.args, " "))))
	fmt.Fprintf(&rendered, "exit: %d\n", result.code)
	fmt.Fprintf(&rendered, "--- stdout\n%s", normalize(result.stdout))
	if result.stdout != "" && !strings.HasSuffix(result.stdout, "\n") {
		rendered.WriteString("\n[no final newline]\n")
	}
	fmt.Fprintf(&rendered, "--- stderr\n%s", normalize(result.stderr))
	if result.stderr != "" && !strings.HasSuffix(result.stderr, "\n") {
		rendered.WriteString("\n[no final newline]\n")
	}
	compareGoldenFile(harness.t, filepath.Join("testdata", "cli", name+".golden"), []byte(rendered.String()))
}

// contractFixture compares machine-readable output with a fixture shared with
// the macOS app's decoder tests, so a field change breaks both languages.
func contractFixture(t *testing.T, name string, payload string) {
	t.Helper()
	compareGoldenFile(t, filepath.Join("..", "..", "testdata", "contract", name), []byte(payload))
}

func compareGoldenFile(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run go test ./cmd/soundconnect -update to create it): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden %s mismatch (run go test ./cmd/soundconnect -update after an intended change)\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// harnessATrustCore is a scriptable aTrust protocol core. Discovery
// advertises NJU-like methods; Authenticate asks the prompter for the factors
// listed in factors before succeeding, and Resume accepts exactly one saved
// client-data value.
type harnessATrustCore struct {
	mu          sync.Mutex
	methods     []backend.AuthenticationMethod
	factors     []string
	authErr     error
	resumable   string
	clientData  string
	logins      int
	resumes     int
	passwords   []string
	codes       []string
	oauthCodes  []string
	blockOnAuth bool
	tunnel      *harnessTunnel
}

func newHarnessATrustCore() *harnessATrustCore {
	return &harnessATrustCore{
		methods: []backend.AuthenticationMethod{
			{Name: "Unified identity", Type: app.ATrustOAuthAuthType, Domain: "oauth-domain", LoginURL: "https://vpn.nju.edu.cn/portal/oauth-login"},
			{Name: "Account password", Type: app.ATrustPasswordAuthType, Domain: "ldap-domain"},
		},
		clientData: "fresh-client-data",
		tunnel:     &harnessTunnel{},
	}
}

func (core *harnessATrustCore) Discover(context.Context, backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	return core.methods, nil
}

func (core *harnessATrustCore) Authenticate(ctx context.Context, request atrustbackend.LoginRequest, prompter atrustbackend.Prompter) (atrustbackend.Session, error) {
	core.mu.Lock()
	core.logins++
	core.mu.Unlock()
	if core.blockOnAuth {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if core.authErr != nil {
		return nil, core.authErr
	}
	for _, factor := range core.factors {
		switch factor {
		case "password":
			password, err := prompter.Password(ctx, atrustbackend.PasswordRequest{Username: request.Username, LoginDomain: request.Method.Domain})
			if err != nil {
				return nil, err
			}
			core.mu.Lock()
			core.passwords = append(core.passwords, string(password))
			core.mu.Unlock()
			clear(password)
		case "sms":
			code, err := prompter.VerificationCode(ctx, atrustbackend.VerificationRequest{Channel: "sms"})
			if err != nil {
				return nil, err
			}
			core.mu.Lock()
			core.codes = append(core.codes, string(code))
			core.mu.Unlock()
			clear(code)
		case "oauth":
			code, err := prompter.OAuthCode(ctx, atrustbackend.OAuthRequest{Endpoint: request.Endpoint, LoginURL: request.Method.LoginURL})
			if err != nil {
				return nil, err
			}
			core.mu.Lock()
			core.oauthCodes = append(core.oauthCodes, code)
			core.mu.Unlock()
		}
	}
	return core.newSession(), nil
}

func (core *harnessATrustCore) Resume(_ context.Context, request atrustbackend.ResumeRequest) (atrustbackend.Session, error) {
	core.mu.Lock()
	core.resumes++
	core.mu.Unlock()
	if core.resumable == "" || string(request.ClientData) != core.resumable {
		return nil, atrustbackend.ErrSessionExpired
	}
	return core.newSession(), nil
}

func (core *harnessATrustCore) newSession() atrustbackend.Session {
	return &harnessSession{clientData: core.clientData, tunnel: core.tunnel}
}

type harnessSession struct {
	clientData string
	tunnel     *harnessTunnel
}

func (session *harnessSession) ClientData() ([]byte, error) { return []byte(session.clientData), nil }

func (session *harnessSession) Resources(context.Context) (atrustbackend.Resources, error) {
	return atrustbackend.Resources{
		DomainRules: []atrustbackend.DomainRule{{Domain: "intranet.example.edu", Protocol: atrustbackend.ProtocolTCP}},
		DNSOverrides: []atrustbackend.DNSOverride{{
			Domain: "intranet.example.edu", Addresses: []netip.Addr{netip.MustParseAddr("10.9.8.7")},
		}},
	}, nil
}

func (session *harnessSession) OpenTunnel(context.Context) (atrustbackend.Tunnel, error) {
	return session.tunnel, nil
}

func (session *harnessSession) Logout(context.Context) error { return nil }
func (session *harnessSession) Close() error                 { return nil }

type harnessTunnel struct{}

func (*harnessTunnel) DialTCP(context.Context, *net.TCPAddr) (net.Conn, error) {
	return nil, errors.New("harness tunnel does not carry traffic")
}

func (*harnessTunnel) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*harnessTunnel) Close() error { return nil }

// waitForRuntime polls the private status socket until a live runtime
// reports state, or fails the test.
func (harness *cliHarness) waitForRuntime(state string) runtimecontrol.Snapshot {
	harness.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := runtimecontrol.Query(runtimecontrol.Path(harness.root))
		if err == nil && snapshot.State == state {
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}
	harness.t.Fatalf("runtime did not reach state %q", state)
	return runtimecontrol.Snapshot{}
}
