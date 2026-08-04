package runtime

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

const communityUTLSClientHelloFixtureHex = "160301006d010000690302" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"204c33495000000000000000000000000000000000000000000000000000000000" +
	"0004000500ff0100001c000f00010100000013001100000e76706e2e6e6a752e6564752e636e"

func TestCommunityUTLSProfileEmittedClientHelloMatchesLiveCompatibleFixture(t *testing.T) {
	client, server := net.Pipe()
	recorded := &recordingConn{Conn: client}
	wire := make(chan []byte, 1)
	go func() {
		defer server.Close()
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			return
		}
		body := make([]byte, int(header[3])<<8|int(header[4]))
		if _, err := io.ReadFull(server, body); err != nil {
			return
		}
		wire <- append(header, body...)
	}()
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return recorded, nil },
		ServerName: "vpn.nju.edu.cn",
	})
	if err != nil {
		t.Fatal(err)
	}
	profile.random = bytes.NewReader(make([]byte, 32))
	fixedNow := time.Now()
	profile.now = func() time.Time { return fixedNow }
	connection, err := profile.Dial(context.Background())
	if connection != nil || err == nil {
		t.Fatalf("connection=%v error=%v", connection, err)
	}
	if stage, ok := failureStageOf(err); !ok || stage != StageProtocolTLSHandshakeFailed {
		t.Fatalf("handshake stage = %q, %t", stage, ok)
	}
	want, err := hex.DecodeString(communityUTLSClientHelloFixtureHex)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-wire; !bytes.Equal(got, want) {
		t.Fatalf("community uTLS ClientHello = %x, want %x", got, want)
	}
	deadlines, closes := recorded.snapshot()
	if len(deadlines) == 0 || !deadlines[0].Equal(fixedNow.Add(gatewayProtocolTimeout)) || closes == 0 {
		t.Fatalf("handshake deadlines=%v closes=%d", deadlines, closes)
	}
}

func TestCommunityUTLSProfileMetadataAndFraming(t *testing.T) {
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID() != ProfileCommunityUTLSCompat || profile.EvidenceID() != EvidenceReverseTestLiveCompat {
		t.Fatalf("profile identity = %q/%q", profile.ID(), profile.EvidenceID())
	}
	if got := profile.SecurityProperties(); !got.Encrypted || !got.PeerVerified {
		t.Fatalf("security properties = %+v", got)
	}
	if got := profile.EstablishedDataFraming(); got.Kind != EstablishedDataFramingRawIPv4 || got.Evidence != EvidenceLevelLiveObservation {
		t.Fatalf("data framing = %+v", got)
	}
	payload := make([]byte, commandRequestSize)
	for index := range payload {
		payload[index] = byte(index)
	}
	for name, write := range map[string]func(io.Writer, []byte) error{
		"initial command":     profile.WriteInitialCommandRequest,
		"established command": profile.WriteEstablishedCommandRequest,
		"initial data":        profile.WriteInitialDataRequest,
	} {
		t.Run(name, func(t *testing.T) {
			var wire bytes.Buffer
			if err := write(&wire, payload); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire.Bytes(), payload) {
				t.Fatalf("wire payload = %x", wire.Bytes())
			}
		})
	}
	reply := bytes.Repeat([]byte{0x5a}, commandReplySize)
	gotReply := make([]byte, commandReplySize)
	if err := profile.ReadInitialCommandReply(bytes.NewReader(reply), gotReply); err != nil || !bytes.Equal(gotReply, reply) {
		t.Fatalf("initial reply=%x error=%v", gotReply, err)
	}
	if err := profile.ReadEstablishedCommandReply(bytes.NewReader(reply), gotReply); err != nil || !bytes.Equal(gotReply, reply) {
		t.Fatalf("established reply=%x error=%v", gotReply, err)
	}
	if code, err := profile.ReadInitialDataReply(bytes.NewReader([]byte{0x02})); err != nil || code != 2 {
		t.Fatalf("data reply=%d error=%v", code, err)
	}
}

func TestCommunityUTLSProfileConsumesCompleteFirstDataReplyChunk(t *testing.T) {
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	handshake := append([]byte{0x01}, bytes.Repeat([]byte{0xa5}, commandReplySize-1)...)
	packet := testIPv4Packet(40, 0x42)
	reader := io.MultiReader(bytes.NewReader(handshake), bytes.NewReader(packet))
	code, err := profile.ReadInitialDataReply(reader)
	if err != nil || code != 0x01 {
		t.Fatalf("data reply=%d error=%v", code, err)
	}
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remaining, packet) {
		t.Fatalf("established stream begins with %x, want IPv4 packet", remaining)
	}
}

func TestCommunityUTLSProfileCertificatePolicyIsExplicitAndScoped(t *testing.T) {
	roots := x509.NewCertPool()
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:        func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
		ServerName:  "vpn.example.edu",
		RootCAs:     roots,
		TLSInsecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := profile.tlsConfig(roots)
	if !config.InsecureSkipVerify || config.ServerName != "vpn.example.edu" || config.RootCAs != roots {
		t.Fatalf("TLS config = %+v", config)
	}
	if config.MinVersion != utls.VersionTLS11 || config.MaxVersion != utls.VersionTLS11 {
		t.Fatalf("TLS versions = %#x..%#x", config.MinVersion, config.MaxVersion)
	}
	if got := profile.SecurityProperties(); !got.Encrypted || got.PeerVerified {
		t.Fatalf("insecure security properties = %+v", got)
	}
}

func TestProtocolRootsComeFromVerifiedHTTPSChainWithoutTrustingLeaf(t *testing.T) {
	leaf := &x509.Certificate{Raw: []byte("leaf cert"), RawSubject: []byte("leaf")}
	intermediate := &x509.Certificate{Raw: []byte("intermediate cert"), RawSubject: []byte("intermediate")}
	root := &x509.Certificate{Raw: []byte("root cert"), RawSubject: []byte("root")}
	pool, added := protocolRootsFromVerifiedChains([][]*x509.Certificate{{leaf, intermediate, root}})
	if !added {
		t.Fatal("verified HTTPS chain produced no protocol roots")
	}
	want := [][]byte{[]byte("intermediate"), []byte("root")}
	if got := pool.Subjects(); !slices.EqualFunc(got, want, bytes.Equal) {
		t.Fatalf("protocol root subjects = %q, want %q", got, want)
	}
	if pool, added := protocolRootsFromVerifiedChains([][]*x509.Certificate{{leaf}}); added || len(pool.Subjects()) != 0 {
		t.Fatal("leaf-only verified chain was promoted to a protocol root")
	}
}

func TestCommunityUTLSProfileClassifiesCertificateFailuresWithoutDetails(t *testing.T) {
	certificate := &x509.Certificate{}
	for name, err := range map[string]error{
		"unknown authority": x509.UnknownAuthorityError{Cert: certificate},
		"hostname":          x509.HostnameError{Certificate: certificate, Host: "secret.example"},
		"invalid":           x509.CertificateInvalidError{Cert: certificate, Reason: x509.Expired},
	} {
		t.Run(name, func(t *testing.T) {
			if got := protocolTLSFailureStage(err); got != StageProtocolTLSCertificateFailed {
				t.Fatalf("stage = %q", got)
			}
			failure := newStageFailure(protocolTLSFailureStage(err), nil)
			if failure.Error() != string(StageProtocolTLSCertificateFailed) || strings.Contains(failure.Error(), "secret") {
				t.Fatalf("published failure = %q", failure)
			}
		})
	}
	if got := protocolTLSFailureStage(errors.New("secret protocol reply")); got != StageProtocolTLSHandshakeFailed {
		t.Fatalf("generic stage = %q", got)
	}
}

func TestCommunityUTLSProfileRawFailureIsClosedAndSanitized(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial: func(context.Context) (net.Conn, error) {
			return recorded, errors.New("secret certificate and gateway reply")
		},
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = profile.Dial(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("raw dial error = %v", err)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("raw connection returned with error was not closed")
	}
}

func TestCommunityUTLSProfileCancellationClosesHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	helloRead := make(chan struct{})
	go func() {
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			return
		}
		body := make([]byte, int(header[3])<<8|int(header[4]))
		_, _ = io.ReadFull(server, body)
		close(helloRead)
		_, _ = io.Copy(io.Discard, server)
	}()
	profile, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return recorded, nil },
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := profile.Dial(ctx)
		result <- err
	}()
	<-helloRead
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled handshake did not return")
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("canceled handshake did not close raw connection")
	}
}
