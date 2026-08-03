package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

type memoryPacketEndpoint struct {
	mu       sync.Mutex
	injected [][]byte
	outbound chan []byte
}

func (endpoint *memoryPacketEndpoint) InjectInbound(packet []byte) error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.injected = append(endpoint.injected, append([]byte(nil), packet...))
	return nil
}

func (endpoint *memoryPacketEndpoint) ReadOutbound(ctx context.Context) ([]byte, error) {
	select {
	case packet := <-endpoint.outbound:
		return packet, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestStreamHandshakeReplyMapping(t *testing.T) {
	for _, test := range []struct {
		kind  StreamKind
		reply byte
	}{{StreamRX, 0x01}, {StreamTX, 0x02}} {
		if err := ValidateStreamReply(test.kind, test.reply); err != nil {
			t.Fatalf("kind %#x reply: %v", test.kind, err)
		}
		if err := ValidateStreamReply(test.kind, test.reply+1); !errors.Is(err, ErrGatewayRejected) {
			t.Fatalf("kind %#x rejection = %v", test.kind, err)
		}
	}
}

func TestRXWorkerInjectsReassembledIPv4Packets(t *testing.T) {
	first := testIPv4Packet(24, 1)
	second := testIPv4Packet(30, 2)
	stream := append(append([]byte(nil), first...), second...)
	endpoint := &memoryPacketEndpoint{outbound: make(chan []byte)}
	worker, err := NewRXWorker(io.NopCloser(bytes.NewReader(stream)), endpoint, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.Run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("RX error = %v", err)
	}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if len(endpoint.injected) != 2 || string(endpoint.injected[0]) != string(first) || string(endpoint.injected[1]) != string(second) {
		t.Fatalf("injected packets = %d", len(endpoint.injected))
	}
}

func TestICMPHeartbeatIsNative76ByteIPv4Packet(t *testing.T) {
	packet, err := BuildICMPHeartbeat(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"), testAgentToken())
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != 76 || packet[0]>>4 != 4 || packet[9] != 1 || packet[20] != 8 {
		t.Fatalf("heartbeat header = %x", packet[:24])
	}
	if checksum(packet[:20]) != 0 || checksum(packet[20:]) != 0 {
		t.Fatal("heartbeat checksum is invalid")
	}
	fixture, err := hex.DecodeString("4500004cbbaa00004001ab040a0000020a000001080075e45555443353414e47464f525343534950434c49454e542122232425262728292a2b2c2d2e2f304c3356504e414243444546474800")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(packet, fixture) {
		t.Fatalf("heartbeat = %x, want %x", packet, fixture)
	}
}

type captureStream struct {
	writes chan []byte
	closed chan struct{}
	once   sync.Once
}

func newCaptureStream() *captureStream {
	return &captureStream{writes: make(chan []byte, 4), closed: make(chan struct{})}
}

func (stream *captureStream) Read([]byte) (int, error) { return 0, io.EOF }
func (stream *captureStream) Write(data []byte) (int, error) {
	stream.writes <- append([]byte(nil), data...)
	return len(data), nil
}
func (stream *captureStream) Close() error {
	stream.once.Do(func() { close(stream.closed) })
	return nil
}

func TestDataStreamFactoryValidatesHandshakeBeforeCreatingWorker(t *testing.T) {
	endpoint := &memoryPacketEndpoint{outbound: make(chan []byte, 1)}
	identity := CommandIdentity{
		AssignedIPv4: netip.MustParseAddr("10.0.0.2"),
		HeartbeatLAN: netip.MustParseAddr("10.0.0.1"),
	}
	rejected := newCaptureStream()
	factory, err := NewDataStreamFactory(func(context.Context, StreamKind) (io.ReadWriteCloser, byte, error) {
		return rejected, 0xff, nil
	}, endpoint, identity, func() ([]byte, error) {
		return BuildICMPHeartbeat(identity.AssignedIPv4, identity.HeartbeatLAN, testAgentToken())
	}, 1024, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.Open(context.Background(), StreamRX); !errors.Is(err, ErrGatewayRejected) {
		t.Fatalf("handshake error = %v", err)
	}
	select {
	case <-rejected.closed:
	case <-time.After(time.Second):
		t.Fatal("rejected stream was not closed")
	}
}

func TestTXWorkerWritesApplicationPacketAndIndependentHeartbeat(t *testing.T) {
	endpoint := &memoryPacketEndpoint{outbound: make(chan []byte, 1)}
	application := testIPv4Packet(32, 7)
	endpoint.outbound <- append([]byte(nil), application...)
	stream := newCaptureStream()
	heartbeat, err := BuildICMPHeartbeat(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"), testAgentToken())
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewTXWorker(stream, endpoint, heartbeat, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- worker.Run(ctx) }()
	seenApplication := false
	seenHeartbeat := false
	deadline := time.After(time.Second)
	for !seenApplication || !seenHeartbeat {
		select {
		case packet := <-stream.writes:
			seenApplication = seenApplication || string(packet) == string(application)
			seenHeartbeat = seenHeartbeat || len(packet) == 76 && packet[9] == 1 && packet[20] == 8
		case <-deadline:
			t.Fatalf("application=%v heartbeat=%v", seenApplication, seenHeartbeat)
		}
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("TX error = %v", err)
	}
}

func testAgentToken() sessiontoken.NativeGatewayToken {
	token := make(sessiontoken.NativeGatewayToken, agentTokenSize)
	for index := range token {
		token[index] = byte(index + 1)
	}
	return token
}
