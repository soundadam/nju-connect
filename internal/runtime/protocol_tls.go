package runtime

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
)

const emptyRenegotiationSCSV uint16 = 0x00ff

type ProtocolTLSDialerConfig struct {
	Dial        CommandDialer
	ServerName  string
	RootCAs     *x509.CertPool
	TLSInsecure bool
}

// ProtocolTLSDialer is intentionally separate from every ordinary HTTPS/TLS
// client. Its legacy ClientHello is scoped only to the gateway command and
// data-stream protocol connections.
type ProtocolTLSDialer struct {
	config ProtocolTLSDialerConfig
	random io.Reader
	now    func() time.Time
	mu     sync.Mutex
}

func NewProtocolTLSDialer(config ProtocolTLSDialerConfig) (*ProtocolTLSDialer, error) {
	if config.Dial == nil {
		return nil, errors.New("raw gateway dialer is required")
	}
	if config.ServerName == "" {
		return nil, errors.New("gateway TLS server name is required")
	}
	return &ProtocolTLSDialer{config: config, random: rand.Reader, now: time.Now}, nil
}

func (dialer *ProtocolTLSDialer) Dial(ctx context.Context) (net.Conn, error) {
	raw, err := dialer.config.Dial(ctx)
	if err != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if raw == nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = raw.Close()
		}
	}()
	stopMonitor := monitorContext(ctx, raw)
	defer stopMonitor()
	if err := raw.SetDeadline(protocolDeadline(ctx, dialer.now())); err != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}

	clientRandom := make([]byte, 32)
	dialer.mu.Lock()
	_, randomErr := io.ReadFull(dialer.random, clientRandom)
	dialer.mu.Unlock()
	if randomErr != nil {
		clear(clientRandom)
		return nil, &TransportFailure{Code: FailureProtocolInvalid}
	}
	config := dialer.tlsConfig()
	connection := utls.UClient(raw, config, utls.HelloCustom)
	if err := connection.ApplyPreset(protocolClientHelloSpec()); err != nil {
		clear(clientRandom)
		return nil, &TransportFailure{Code: FailureProtocolInvalid}
	}
	copy(connection.HandshakeState.Hello.Random, clientRandom)
	clear(clientRandom)
	sessionID := make([]byte, 32)
	copy(sessionID, []byte("L3IP"))
	connection.HandshakeState.Hello.SessionId = sessionID
	if err := connection.HandshakeContext(ctx); err != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if ctx.Err() != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	keepOpen = true
	return connection, nil
}

func (dialer *ProtocolTLSDialer) tlsConfig() *utls.Config {
	return &utls.Config{
		ServerName:         dialer.config.ServerName,
		RootCAs:            dialer.config.RootCAs,
		InsecureSkipVerify: dialer.config.TLSInsecure,
		MinVersion:         utls.VersionTLS11,
		MaxVersion:         utls.VersionTLS11,
	}
}

func protocolClientHelloSpec() *utls.ClientHelloSpec {
	return &utls.ClientHelloSpec{
		TLSVersMin:         utls.VersionTLS11,
		TLSVersMax:         utls.VersionTLS11,
		CipherSuites:       []uint16{utls.TLS_RSA_WITH_RC4_128_SHA, emptyRenegotiationSCSV},
		CompressionMethods: []uint8{0},
		Extensions: []utls.TLSExtension{
			&utls.GenericExtension{Id: 0x000f, Data: []byte{0x01}},
		},
	}
}

func monitorContext(ctx context.Context, closer io.Closer) func() {
	stopped := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-stopped:
		}
	}()
	return func() {
		once.Do(func() { close(stopped) })
		<-done
	}
}
