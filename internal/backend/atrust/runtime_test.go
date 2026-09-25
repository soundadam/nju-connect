package atrustbackend

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/backend"
)

type fakeCore struct {
	resumeErr  error
	authErr    error
	session    *fakeSession
	resumed    [][]byte
	authCalled bool
}

func (core *fakeCore) Discover(context.Context, backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	return []backend.AuthenticationMethod{{Type: "auth/psw", Domain: "ldap"}}, nil
}

func (core *fakeCore) Authenticate(ctx context.Context, request LoginRequest, prompter Prompter) (Session, error) {
	core.authCalled = true
	if core.authErr != nil {
		return nil, core.authErr
	}
	password, err := prompter.Password(ctx, PasswordRequest{Username: request.Username, LoginDomain: request.Method.Domain})
	if err != nil {
		return nil, err
	}
	clear(password)
	return core.session, nil
}

func (core *fakeCore) Resume(_ context.Context, request ResumeRequest) (Session, error) {
	core.resumed = append(core.resumed, append([]byte(nil), request.ClientData...))
	if core.resumeErr != nil {
		return nil, core.resumeErr
	}
	return core.session, nil
}

type fakeSession struct {
	resources Resources
	tunnel    *fakeTunnel
	closed    bool
}

func (session *fakeSession) ClientData() ([]byte, error) { return []byte("opaque"), nil }
func (session *fakeSession) Resources(context.Context) (Resources, error) {
	return session.resources, nil
}
func (session *fakeSession) OpenTunnel(context.Context) (Tunnel, error) { return session.tunnel, nil }
func (session *fakeSession) Logout(context.Context) error               { return nil }
func (session *fakeSession) Close() error {
	session.closed = true
	return nil
}

type fakeTunnel struct {
	mu      sync.Mutex
	dialed  []string
	runErr  chan error
	closed  bool
	respond []byte
}

func (tunnel *fakeTunnel) DialTCP(_ context.Context, destination *net.TCPAddr) (net.Conn, error) {
	tunnel.mu.Lock()
	tunnel.dialed = append(tunnel.dialed, destination.String())
	tunnel.mu.Unlock()
	local, remote := net.Pipe()
	go func() {
		defer remote.Close()
		request := make([]byte, 4)
		if _, err := io.ReadFull(remote, request); err == nil {
			_, _ = remote.Write(tunnel.respond)
		}
	}()
	return local, nil
}

func (tunnel *fakeTunnel) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-tunnel.runErr:
		return err
	}
}

func (tunnel *fakeTunnel) Close() error {
	tunnel.closed = true
	return nil
}

func (tunnel *fakeTunnel) destinations() []string {
	tunnel.mu.Lock()
	defer tunnel.mu.Unlock()
	return append([]string(nil), tunnel.dialed...)
}

type unusedDirect struct{ t *testing.T }

func (direct unusedDirect) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	direct.t.Errorf("unexpected direct dial to %s", address)
	return nil, errors.New("direct path is not expected")
}

func newFakeCore() *fakeCore {
	return &fakeCore{session: &fakeSession{
		resources: Resources{
			DomainRules:  []DomainRule{{Domain: "intranet.example.edu", Protocol: ProtocolTCP}},
			DNSOverrides: []DNSOverride{{Domain: "intranet.example.edu", Addresses: []netip.Addr{netip.MustParseAddr("10.9.8.7")}}},
		},
		tunnel: &fakeTunnel{runErr: make(chan error, 1), respond: []byte("pong")},
	}}
}

func testConnectConfig(core Core, t *testing.T) ConnectConfig {
	return ConnectConfig{
		Core: core,
		Login: LoginRequest{
			Endpoint: backend.ATrustEndpoint("vpn.nju.edu.cn", 443),
			Method:   backend.AuthenticationMethod{Type: "auth/psw", Domain: "ldap"},
			Username: "student",
		},
		Prompter: PrompterFuncs{OnPassword: func(context.Context, PasswordRequest) ([]byte, error) {
			return []byte("synthetic-password"), nil
		}},
		SOCKSListen: "127.0.0.1:0",
		Direct:      unusedDirect{t: t},
	}
}

func TestDiscoveryRejectsInvalidEndpoint(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []backend.Endpoint{{Port: 443}, {Host: "example.edu"}, {Host: "example.edu", Port: 65536}} {
		if _, err := (Discovery{Core: newFakeCore()}).Discover(context.Background(), endpoint); err == nil {
			t.Fatalf("Discover accepted endpoint %+v", endpoint)
		}
	}
}

func TestConnectPrefersSavedClientData(t *testing.T) {
	t.Parallel()
	core := newFakeCore()
	config := testConnectConfig(core, t)
	config.SavedClientData = []byte("saved")
	connection, err := Connect(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if !connection.Resumed() || core.authCalled || len(core.resumed) != 1 || string(core.resumed[0]) != "saved" {
		t.Fatalf("resumed=%t auth=%t resume calls=%q", connection.Resumed(), core.authCalled, core.resumed)
	}
}

func TestConnectFallsBackToLoginOnlyWhenSavedSessionExpired(t *testing.T) {
	t.Parallel()
	core := newFakeCore()
	core.resumeErr = ErrSessionExpired
	config := testConnectConfig(core, t)
	config.SavedClientData = []byte("saved")
	connection, err := Connect(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if connection.Resumed() || !core.authCalled {
		t.Fatalf("resumed=%t auth=%t", connection.Resumed(), core.authCalled)
	}

	transient := newFakeCore()
	transient.resumeErr = errors.New("gateway unreachable")
	config = testConnectConfig(transient, t)
	config.SavedClientData = []byte("saved")
	if _, err := Connect(context.Background(), config); err == nil || transient.authCalled {
		t.Fatalf("transient resume error = %v, auth=%t", err, transient.authCalled)
	}
}

func TestConnectSurfacesUnavailableFactor(t *testing.T) {
	t.Parallel()
	config := testConnectConfig(newFakeCore(), t)
	config.Prompter = PrompterFuncs{}
	if _, err := Connect(context.Background(), config); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestConnectionRoutesSOCKSDomainResourceThroughTunnel(t *testing.T) {
	t.Parallel()
	core := newFakeCore()
	connection, err := Connect(context.Background(), testConnectConfig(core, t))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- connection.Run(ctx) }()

	client := dialSOCKSDomain(t, connection.SOCKSAddr().String(), "intranet.example.edu", 443)
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(client, response); err != nil || !bytes.Equal(response, []byte("pong")) {
		t.Fatalf("response = %q, %v", response, err)
	}
	_ = client.Close()
	if got := core.session.tunnel.destinations(); len(got) != 1 || got[0] != "10.9.8.7:443" {
		t.Fatalf("tunnel destinations = %q", got)
	}
	deadline := time.Now().Add(time.Second)
	for connection.Traffic().UploadBytes != 4 || connection.Traffic().DownloadBytes != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("traffic = %+v", connection.Traffic())
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() after cancel = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if !core.session.closed || !core.session.tunnel.closed {
		t.Fatal("Close() did not release session and tunnel")
	}
}

func TestConnectionRunReportsTunnelFailure(t *testing.T) {
	t.Parallel()
	core := newFakeCore()
	connection, err := Connect(context.Background(), testConnectConfig(core, t))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	failure := errors.New("tunnel node lost")
	core.session.tunnel.runErr <- failure
	if err := connection.Run(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("Run() = %v", err)
	}
}

func dialSOCKSDomain(t *testing.T, proxy, name string, port uint16) net.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := connection.Write([]byte{5, 1, 0}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(connection, method); err != nil || method[1] != 0 {
		t.Fatalf("method = %v, %v", method, err)
	}
	request := append([]byte{5, 1, 0, 3, byte(len(name))}, name...)
	request = binary.BigEndian.AppendUint16(request, port)
	if _, err := connection.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(connection, reply); err != nil || reply[1] != 0 {
		t.Fatalf("CONNECT reply = %v, %v", reply, err)
	}
	return connection
}
