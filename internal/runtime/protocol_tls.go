package runtime

import (
	"context"
	"crypto/rand"
	"crypto/tls"
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
	Dial             CommandDialer
	ServerName       string
	RootCAs          *x509.CertPool
	TLSInsecure      bool
	BootstrapRootCAs bool
}

// ProtocolTLSDialer implements the community uTLS compatibility profile. It
// is retained as a live-compatible profile, not treated as a cross-version
// description of every EasyConnect client.
type ProtocolTLSDialer struct {
	config ProtocolTLSDialerConfig
	random io.Reader
	now    func() time.Time
	mu     sync.Mutex

	rootMu        sync.Mutex
	protocolRoots *x509.CertPool
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
	rootCAs, err := dialer.rootCAs(ctx)
	if err != nil {
		return nil, err
	}
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
	connection := utls.UClient(raw, dialer.tlsConfig(rootCAs), utls.HelloCustom)
	if err := connection.ApplyPreset(protocolClientHelloSpec(dialer.config.ServerName)); err != nil {
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
	// Reverse testing established that the live-compatible path reads the complete first TLS application
	// chunk into a 1500-byte buffer and validates only its first byte. Reading a
	// single byte here leaves the rest of the stream-handshake reply queued for
	// the IPv4 decoder, which then correctly rejects it as non-IPv4 data.
	var reply [1500]byte
	count, err := io.ReadAtLeast(reader, reply[:], 1)
	if err != nil {
		clear(reply[:])
		return 0, err
	}
	code := uint32(reply[0])
	clear(reply[:count])
	return code, nil
}

// rootCAs bootstraps the legacy L3IP handshake from a separately verified,
// modern TLS handshake to the same gateway. Some gateways omit their
// intermediate certificate only for the legacy EasyConnect ClientHello. Go
// does not fetch AIA intermediates, so verification otherwise starts failing
// when that gateway certificate chain rotates even though HTTPS remains valid.
func (dialer *ProtocolTLSDialer) rootCAs(ctx context.Context) (*x509.CertPool, error) {
	if dialer.config.TLSInsecure || !dialer.config.BootstrapRootCAs {
		return dialer.config.RootCAs, nil
	}
	dialer.rootMu.Lock()
	defer dialer.rootMu.Unlock()
	if dialer.protocolRoots != nil {
		return dialer.protocolRoots, nil
	}
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
	defer raw.Close()
	stopMonitor := monitorContext(ctx, raw)
	defer stopMonitor()
	if err := raw.SetDeadline(protocolDeadline(ctx, dialer.now())); err != nil {
		return nil, newStageFailure(StageProtocolTLSHandshakeFailed, nil)
	}
	connection := tls.Client(raw, &tls.Config{
		ServerName: dialer.config.ServerName,
		RootCAs:    dialer.config.RootCAs,
		MinVersion: tls.VersionTLS12,
	})
	if err := connection.HandshakeContext(ctx); err != nil {
		return nil, newStageFailure(protocolTLSFailureStage(err), nil)
	}
	state := connection.ConnectionState()
	roots, added := protocolRootsFromVerifiedChains(state.VerifiedChains)
	if !added {
		return nil, newStageFailure(StageProtocolTLSCertificateFailed, nil)
	}
	dialer.protocolRoots = roots
	return roots, nil
}

func protocolRootsFromVerifiedChains(chains [][]*x509.Certificate) (*x509.CertPool, bool) {
	roots := x509.NewCertPool()
	added := false
	for _, chain := range chains {
		for _, certificate := range chain[1:] {
			roots.AddCert(certificate)
			added = true
		}
	}
	return roots, added
}

func (dialer *ProtocolTLSDialer) tlsConfig(rootCAs *x509.CertPool) *utls.Config {
	return &utls.Config{
		ServerName:         dialer.config.ServerName,
		RootCAs:            rootCAs,
		InsecureSkipVerify: dialer.config.TLSInsecure, // #nosec G402 -- compatibility downgrade is explicit configuration.
		MinVersion:         utls.VersionTLS11,
		MaxVersion:         utls.VersionTLS11,
	}
}

func protocolClientHelloSpec(serverName string) *utls.ClientHelloSpec {
	spec := &utls.ClientHelloSpec{
		TLSVersMin:         utls.VersionTLS11,
		TLSVersMax:         utls.VersionTLS11,
		CipherSuites:       []uint16{utls.TLS_RSA_WITH_RC4_128_SHA, emptyRenegotiationSCSV},
		CompressionMethods: []uint8{0},
		Extensions: []utls.TLSExtension{
			&utls.GenericExtension{Id: 0x000f, Data: []byte{0x01}},
		},
	}
	if serverName != "" {
		spec.Extensions = append(spec.Extensions, &utls.SNIExtension{ServerName: serverName})
	}
	return spec
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
