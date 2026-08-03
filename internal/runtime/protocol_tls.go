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

// ProtocolTLSDialer implements the community uTLS compatibility profile. It
// is retained as a live-compatible profile, not treated as a cross-version
// description of every EasyConnect client.
type ProtocolTLSDialer struct {
	config ProtocolTLSDialerConfig
	random io.Reader
	now    func() time.Time
	mu     sync.Mutex
}

var _ ProtocolProfile = (*ProtocolTLSDialer)(nil)

func NewProtocolTLSDialer(config ProtocolTLSDialerConfig) (*ProtocolTLSDialer, error) {
	if config.Dial == nil {
		return nil, errors.New("raw gateway dialer is required")
	}
	if config.ServerName == "" {
		return nil, errors.New("gateway TLS server name is required")
	}
	return &ProtocolTLSDialer{config: config, random: rand.Reader, now: time.Now}, nil
}

func (dialer *ProtocolTLSDialer) ID() ProtocolProfileID {
	return ProfileCommunityUTLSCompat
}

func (dialer *ProtocolTLSDialer) EvidenceID() ProtocolEvidenceID {
	return EvidenceReverseTestLiveCompat
}

func (dialer *ProtocolTLSDialer) SecurityProperties() ProtocolSecurityProperties {
	return ProtocolSecurityProperties{
		Encrypted:    true,
		PeerVerified: !dialer.config.TLSInsecure,
	}
}

func (dialer *ProtocolTLSDialer) EstablishedDataFraming() EstablishedDataFramingEvidence {
	return EstablishedDataFramingEvidence{
		Kind:     EstablishedDataFramingRawIPv4,
		Evidence: EvidenceLevelLiveObservation,
	}
}

func (dialer *ProtocolTLSDialer) Dial(ctx context.Context) (net.Conn, error) {
	raw, err := dialer.config.Dial(ctx)
	if err != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, newStageFailure(StageUpstreamConnectFailed, nil)
	}
	if raw == nil {
		return nil, newStageFailure(StageUpstreamConnectFailed, nil)
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
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}

	clientRandom := make([]byte, 32)
	dialer.mu.Lock()
	_, randomErr := io.ReadFull(dialer.random, clientRandom)
	dialer.mu.Unlock()
	if randomErr != nil {
		clear(clientRandom)
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	connection := utls.UClient(raw, dialer.tlsConfig(), utls.HelloCustom)
	if err := connection.ApplyPreset(protocolClientHelloSpec()); err != nil {
		clear(clientRandom)
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	if err := connection.SetClientRandom(clientRandom); err != nil {
		clear(clientRandom)
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	clear(clientRandom)
	sessionID := make([]byte, 32)
	copy(sessionID, []byte("L3IP"))
	connection.HandshakeState.Hello.SessionId = sessionID
	if err := connection.HandshakeContext(ctx); err != nil {
		return nil, newStageFailure(protocolTLSFailureStage(err), nil)
	}
	if ctx.Err() != nil {
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	keepOpen = true
	return connection, nil
}

func protocolTLSFailureStage(err error) FailureStage {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &certificateInvalid) {
		return StageProtocolTLSCertificateFailed
	}
	return StageProtocolTLSHandshakeFailed
}

func (dialer *ProtocolTLSDialer) WriteInitialCommandRequest(writer io.Writer, payload []byte) error {
	return writeRawProfilePayload(writer, payload, commandRequestSize)
}

func (dialer *ProtocolTLSDialer) ReadInitialCommandReply(reader io.Reader, payload []byte) error {
	return readRawProfilePayload(reader, payload, commandReplySize)
}

func (dialer *ProtocolTLSDialer) WriteEstablishedCommandRequest(writer io.Writer, payload []byte) error {
	return writeRawProfilePayload(writer, payload, commandRequestSize)
}

func (dialer *ProtocolTLSDialer) ReadEstablishedCommandReply(reader io.Reader, payload []byte) error {
	return readRawProfilePayload(reader, payload, commandReplySize)
}

func (dialer *ProtocolTLSDialer) WriteInitialDataRequest(writer io.Writer, payload []byte) error {
	return writeRawProfilePayload(writer, payload, commandRequestSize)
}

func (dialer *ProtocolTLSDialer) ReadInitialDataReply(reader io.Reader) (uint32, error) {
	var reply [1]byte
	if _, err := io.ReadFull(reader, reply[:]); err != nil {
		return 0, err
	}
	return uint32(reply[0]), nil
}

func (dialer *ProtocolTLSDialer) tlsConfig() *utls.Config {
	return &utls.Config{
		ServerName:         dialer.config.ServerName,
		RootCAs:            dialer.config.RootCAs,
		InsecureSkipVerify: dialer.config.TLSInsecure, // #nosec G402 -- compatibility downgrade is explicit configuration.
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

func writeRawProfilePayload(writer io.Writer, payload []byte, expected int) error {
	if len(payload) != expected {
		return errors.New("protocol payload has an invalid length")
	}
	return writeFull(writer, payload)
}

func readRawProfilePayload(reader io.Reader, payload []byte, expected int) error {
	if len(payload) != expected {
		return errors.New("protocol payload has an invalid length")
	}
	_, err := io.ReadFull(reader, payload)
	return err
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
