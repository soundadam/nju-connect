package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/nativeapp"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

const nativeSSLContextFixture = "000102030405060708090a0b0c0d0e0f" +
	"101112131415161718191a1b1c1d1e1f" +
	"202122232425262728292a2b2c2d2e2f" +
	"303132333435363738393a3b3c3d3e3f"

func TestNativeConnectWiresAuthenticatedSessionWithoutLeakingMaterial(t *testing.T) {
	server := newNativeGatewayTestServer(t)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	writeNativeCommandState(t, worktree, config.Config{
		Server:            parsed.Host,
		Username:          "fixture-account",
		SOCKSListen:       "127.0.0.1:1081",
		TLSInsecure:       true,
		NativeTLSInsecure: true,
	}, []byte("fixture-password"))

	ctx, cancel := context.WithCancel(context.Background())
	application := &fakeNativeApplication{cancel: cancel}
	var borrowed sessiontoken.NativeGatewayToken
	factory := func(sessionConfig nativeapp.SessionConfig) (nativeApplicationSession, error) {
		borrowed = sessionConfig.NativeGatewayToken
		want := append(make([]byte, 32), []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
			0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f}...)
		if !bytes.Equal(sessionConfig.NativeGatewayToken, want) {
			t.Fatal("native session received the wrong SSL-context token")
		}
		if !sessionConfig.Settings.TLSInsecure || !sessionConfig.Settings.NativeTLSInsecure || !sessionConfig.Plan.BoundaryReady {
			t.Fatalf("native session config = %+v", sessionConfig.Plan)
		}
		if sessionConfig.NativeProfile != runtime.ProfileCommunityUTLSCompat {
			t.Fatalf("native profile = %q", sessionConfig.NativeProfile)
		}
		application.profile = runtime.ProtocolProfileMetadata{
			ID:       runtime.ProfileCommunityUTLSCompat,
			Evidence: runtime.EvidenceReverseTestLiveCompat,
			Security: runtime.ProtocolSecurityProperties{Encrypted: true, PeerVerified: false},
		}
		sessionConfig.Observer.StateChanged(nativeapp.StateConnecting)
		sessionConfig.Observer.CommandFailed(nativeapp.CommandFailure{
			Attempt: 1,
			Stage:   nativeapp.CommandProtocolTLSHandshakeFailed,
			At:      time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC),
		})
		sessionConfig.Observer.DataFailed(nativeapp.DataRXHandshakeFailed)
		sessionConfig.Observer.SOCKSListening("127.0.0.1:1081")
		sessionConfig.Observer.AccessEvidence(true)
		sessionConfig.Observer.TrafficChanged(nativeapp.TrafficSnapshot{UploadBytes: 7, DownloadBytes: 9})
		return application, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNativeConnectContext(ctx, []string{"--worktree", worktree}, &stdout, &stderr, factory)
	if code != 0 {
		t.Fatalf("native-connect exit = %d, stderr = %q", code, stderr.String())
	}
	if !application.closed {
		t.Fatal("native application session was not closed")
	}
	if !bytes.Equal(borrowed, make([]byte, sessiontoken.NativeGatewayTokenSize)) {
		t.Fatal("borrowed native gateway token was not cleared after construction")
	}
	for _, expected := range []string{
		"authentication: accepted\n",
		"native-profile: community-utls\n",
		"native-evidence: reverse_test_live_2026_07_18\n",
		"native-security: encrypted=true peer_verified=false\n",
		"state: connecting\n",
		"command: at=2026-08-03T12:00:00Z attempt=1 stage=protocol_tls_handshake_failed\n",
		"data: stage=rx_handshake_failed\n",
		"socks: 127.0.0.1:1081\n",
		"access: available=true\n",
		"traffic: upload=7 download=9 active=0 total=0\n",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("stdout %q lacks %q", stdout.String(), expected)
		}
	}
	for _, sensitive := range []string{
		"fedcba9876543210",
		nativeSSLContextFixture,
		"fixture-account",
		"fixture-password",
	} {
		if strings.Contains(stdout.String(), sensitive) || strings.Contains(stderr.String(), sensitive) {
			t.Fatalf("command output exposed sensitive fixture %q", sensitive)
		}
	}
}

func TestNativeConnectPreflightsUpstreamBeforeReadingCredential(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	paths, err := config.LocalPaths(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Replace(paths.Config, config.Config{
		Server:        "vpn.example.edu",
		Username:      "fixture-account",
		SOCKSListen:   "127.0.0.1:1081",
		UpstreamProxy: "socks5://" + address,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNativeConnectContext(context.Background(), []string{"--worktree", worktree}, &stdout, &stderr,
		func(nativeapp.SessionConfig) (nativeApplicationSession, error) {
			t.Fatal("native session factory was reached")
			return nil, nil
		})
	if code != 1 || !strings.Contains(stderr.String(), "upstream preflight:") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "credential") {
		t.Fatalf("credential was consulted before upstream preflight: %q", stderr.String())
	}
}

func TestNativeConnectRejectsDevelopmentOnlyProfileFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	called := false
	code := runNativeConnectContext(context.Background(), []string{"--native-profile", "community-utls"}, &stdout, &stderr,
		func(nativeapp.SessionConfig) (nativeApplicationSession, error) {
			called = true
			return nil, nil
		})
	if code != 2 || called || !strings.Contains(stderr.String(), "flag provided but not defined: -native-profile") {
		t.Fatalf("exit=%d called=%t stderr=%q", code, called, stderr.String())
	}
}

func TestNativeConnectReturnsActionableRenewalWithoutReauthentication(t *testing.T) {
	var stderr bytes.Buffer
	code := reportNativeRunResult(context.Background(),
		&runtime.RenewalRequired{Reason: runtime.RenewalGatewayRejected}, &stderr)
	if code != 1 || stderr.String() != "renewal_required: run native-connect again to reauthenticate\n" {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestNativeConnectSanitizesUnknownTransportFailure(t *testing.T) {
	const sensitive = "gateway-reply-secret"
	var stderr bytes.Buffer
	code := reportNativeRunResult(context.Background(),
		&runtime.TransportFailure{Code: runtime.FailureCode(sensitive)}, &stderr)
	if code != 1 || stderr.String() != "native transport: runtime_stopped\n" || strings.Contains(stderr.String(), sensitive) {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

type fakeNativeApplication struct {
	cancel  context.CancelFunc
	runErr  error
	closed  bool
	profile runtime.ProtocolProfileMetadata
}

func (application *fakeNativeApplication) Run(context.Context) error {
	if application.cancel != nil {
		application.cancel()
		return context.Canceled
	}
	return application.runErr
}

func (application *fakeNativeApplication) Close() error {
	application.closed = true
	return nil
}

func (application *fakeNativeApplication) Profile() runtime.ProtocolProfileMetadata {
	return application.profile
}

func newNativeGatewayTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/por/login_auth.csp":
			fmt.Fprintf(writer, "<Auth><ErrorCode>1</ErrorCode><TwfID>fedcba9876543210</TwfID><RSA_ENCRYPT_KEY>%s</RSA_ENCRYPT_KEY><RSA_ENCRYPT_EXP>65537</RSA_ENCRYPT_EXP><CSRF_RAND_CODE>fixture-nonce</CSRF_RAND_CODE></Auth>", privateKey.N.Text(16))
		case "/por/login_psw.csp":
			fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode></Auth>")
		case "/por/conf.csp":
			fmt.Fprintf(writer, `<Conf><Other sslctx="%s"/></Conf>`, nativeSSLContextFixture)
		case "/por/rclist.csp":
			fmt.Fprint(writer, `<Resource><Rcs><Rc type="2"/></Rcs></Resource>`)
		default:
			http.NotFound(writer, request)
		}
	}))
}

func writeNativeCommandState(t *testing.T, worktree string, configured config.Config, password []byte) {
	t.Helper()
	paths, err := config.LocalPaths(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Replace(paths.Config, configured); err != nil {
		t.Fatal(err)
	}
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(password); err != nil {
		t.Fatal(err)
	}
}
