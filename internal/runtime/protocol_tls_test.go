package runtime

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestProtocolTLSClientHelloExactLayoutAndHandshakeFailureClose(t *testing.T) {
	client, server := net.Pipe()
	recorded := &recordingConn{Conn: client}
	hello := make(chan []byte, 1)
	go func() {
		defer server.Close()
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint16(header[3:5])))
		if _, err := io.ReadFull(server, body); err != nil {
			return
		}
		hello <- append(header, body...)
	}()
	dialer, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return recorded, nil },
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	dialer.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 32))
	fixedNow := time.Now()
	dialer.now = func() time.Time { return fixedNow }
	connection, err := dialer.Dial(context.Background())
	if connection != nil || err == nil {
		t.Fatalf("connection=%v error=%v", connection, err)
	}
	if stage, ok := failureStageOf(err); !ok || stage != StageProtocolTLSHandshakeFailed {
		t.Fatalf("handshake stage = %q, %t", stage, ok)
	}
	if strings.Contains(err.Error(), "vpn.example") {
		t.Fatalf("handshake error exposed details: %v", err)
	}
	assertProtocolClientHello(t, <-hello)
	deadlines, closes := recorded.snapshot()
	if len(deadlines) == 0 || !deadlines[0].Equal(fixedNow.Add(15*time.Second)) {
		t.Fatalf("handshake deadlines = %v", deadlines)
	}
	if closes == 0 {
		t.Fatal("failed TLS handshake did not close raw connection")
	}
}

func assertProtocolClientHello(t *testing.T, record []byte) {
	t.Helper()
	if len(record) < 9 || record[0] != 22 || record[5] != 1 {
		t.Fatalf("TLS record prefix = %x", record)
	}
	handshakeLength := int(record[6])<<16 | int(record[7])<<8 | int(record[8])
	if int(binary.BigEndian.Uint16(record[3:5])) != len(record)-5 || handshakeLength != len(record)-9 {
		t.Fatalf("TLS record lengths = %x", record[:9])
	}
	hello := record[9:]
	if !bytes.Equal(hello[0:2], []byte{0x03, 0x02}) {
		t.Fatalf("ClientHello version = %x", hello[0:2])
	}
	if !bytes.Equal(hello[2:34], bytes.Repeat([]byte{0x42}, 32)) {
		t.Fatalf("ClientHello random = %x", hello[2:34])
	}
	if hello[34] != 32 || !bytes.Equal(hello[35:39], []byte("L3IP")) || !bytes.Equal(hello[39:67], make([]byte, 28)) {
		t.Fatalf("SessionId = %x", hello[34:67])
	}
	position := 67
	cipherLength := int(binary.BigEndian.Uint16(hello[position : position+2]))
	position += 2
	if cipherLength != 4 || !bytes.Equal(hello[position:position+4], []byte{0x00, 0x05, 0x00, 0xff}) {
		t.Fatalf("cipher suites = %x", hello[position:position+cipherLength])
	}
	position += cipherLength
	if !bytes.Equal(hello[position:position+2], []byte{1, 0}) {
		t.Fatalf("compression = %x", hello[position:position+2])
	}
	position += 2
	extensionLength := int(binary.BigEndian.Uint16(hello[position : position+2]))
	position += 2
	if extensionLength != 5 || !bytes.Equal(hello[position:position+5], []byte{0, 15, 0, 1, 1}) {
		t.Fatalf("extensions = %x", hello[position:position+extensionLength])
	}
	position += extensionLength
	if position != len(hello) {
		t.Fatalf("unexpected ClientHello tail = %x", hello[position:])
	}
}

func TestProtocolTLSUsesNormalCertificateVerification(t *testing.T) {
	roots := x509.NewCertPool()
	dialer, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
		ServerName: "vpn.example.edu",
		RootCAs:    roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := dialer.tlsConfig()
	if config.InsecureSkipVerify || config.ServerName != "vpn.example.edu" || config.RootCAs != roots {
		t.Fatalf("certificate verification config = %+v", config)
	}
	if config.MinVersion != utls.VersionTLS11 || config.MaxVersion != utls.VersionTLS11 {
		t.Fatalf("TLS versions = %#x..%#x", config.MinVersion, config.MaxVersion)
	}
}

func TestProtocolTLSInsecureCompatibilityIsExplicitAndScoped(t *testing.T) {
	dialer, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:        func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
		ServerName:  "vpn.example.edu",
		TLSInsecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dialer.tlsConfig().InsecureSkipVerify {
		t.Fatal("explicit gateway protocol TLS compatibility was not applied")
	}
}

func TestProtocolTLSRawFailureIsClosedAndSanitized(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	dialer, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial: func(context.Context) (net.Conn, error) {
			return recorded, errors.New("secret certificate and gateway reply")
		},
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dialer.Dial(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("raw dial error = %v", err)
	}
	if stage, ok := failureStageOf(err); !ok || stage != StageUpstreamConnectFailed {
		t.Fatalf("raw dial stage = %q, %t", stage, ok)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("raw connection returned with error was not closed")
	}
}

func TestProtocolTLSCancellationClosesHandshake(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	helloRead := make(chan struct{})
	go func() {
		header := make([]byte, 5)
		if _, err := io.ReadFull(server, header); err != nil {
			return
		}
		body := make([]byte, int(binary.BigEndian.Uint16(header[3:5])))
		_, _ = io.ReadFull(server, body)
		close(helloRead)
		_, _ = io.Copy(io.Discard, server)
	}()
	dialer, err := NewProtocolTLSDialer(ProtocolTLSDialerConfig{
		Dial:       func(context.Context) (net.Conn, error) { return recorded, nil },
		ServerName: "vpn.example.edu",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := dialer.Dial(ctx)
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
