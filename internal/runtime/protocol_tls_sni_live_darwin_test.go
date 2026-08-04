//go:build darwin && liveprobe

package runtime

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/sys/unix"
)

// TestLiveSNICompatibility is an explicit, credential-free gateway probe. It
// compares the legacy no-SNI baseline with the production SNI ClientHello, is
// excluded from normal builds and tests by the liveprobe build tag, and does
// nothing unless SOUNDCONNECT_LIVE_GATEWAY is provided.
func TestLiveSNICompatibility(t *testing.T) {
	target := os.Getenv("SOUNDCONNECT_LIVE_GATEWAY")
	if target == "" {
		t.Skip("SOUNDCONNECT_LIVE_GATEWAY is required")
	}
	serverName, _, err := net.SplitHostPort(target)
	if err != nil || serverName == "" {
		t.Fatal("SOUNDCONNECT_LIVE_GATEWAY must be host:port")
	}

	rawDial, err := liveProbeDialer(target, os.Getenv("SOUNDCONNECT_LIVE_INTERFACE"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	baselineErr := liveProbeHandshake(ctx, rawDial, serverName, nil, true, false)
	if baselineErr != nil {
		t.Fatalf("no-sni insecure result=rejected: %v", baselineErr)
	}
	t.Log("no-sni insecure result=accepted")

	if err := liveProbeHandshake(ctx, rawDial, serverName, nil, true, true); err != nil {
		t.Logf("sni insecure result=rejected: %v", err)
	} else {
		t.Log("sni insecure result=accepted")
	}

	bootstrap, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial: rawDial, ServerName: serverName, BootstrapRootCAs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	roots, err := bootstrap.rootCAs(ctx)
	if err != nil {
		t.Fatalf("verified root bootstrap failed: %v", err)
	}
	if err := liveProbeHandshake(ctx, rawDial, serverName, roots, false, true); err != nil {
		t.Logf("sni verified result=rejected: %v", err)
	} else {
		t.Log("sni verified result=accepted")
	}
}

func liveProbeDialer(target, interfaceName string) (CommandDialer, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if interfaceName != "" {
		device, err := net.InterfaceByName(interfaceName)
		if err != nil {
			return nil, err
		}
		dialer.Control = func(_, _ string, raw syscall.RawConn) error {
			var controlErr error
			if err := raw.Control(func(fd uintptr) {
				controlErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, device.Index)
			}); err != nil {
				return err
			}
			return controlErr
		}
	}
	return func(ctx context.Context) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", target)
	}, nil
}

func liveProbeHandshake(
	ctx context.Context,
	rawDial CommandDialer,
	serverName string,
	roots *x509.CertPool,
	insecure bool,
	withSNI bool,
) error {
	raw, err := rawDial(ctx)
	if err != nil {
		return err
	}
	defer raw.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := raw.SetDeadline(deadline); err != nil {
			return err
		}
	}

	config := &utls.Config{
		ServerName: serverName, RootCAs: roots, InsecureSkipVerify: insecure,
		MinVersion: utls.VersionTLS11, MaxVersion: utls.VersionTLS11,
	}
	connection := utls.UClient(raw, config, utls.HelloCustom)
	specServerName := ""
	if withSNI {
		specServerName = serverName
	}
	spec := protocolClientHelloSpec(specServerName)
	if err := connection.ApplyPreset(spec); err != nil {
		return err
	}
	clientRandom := make([]byte, 32)
	defer clear(clientRandom)
	if _, err := io.ReadFull(rand.Reader, clientRandom); err != nil {
		return err
	}
	if err := connection.SetClientRandom(clientRandom); err != nil {
		return err
	}
	sessionID := make([]byte, 32)
	copy(sessionID, []byte("L3IP"))
	connection.HandshakeState.Hello.SessionId = sessionID
	return connection.HandshakeContext(ctx)
}
