package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

func ExpectedStreamReply(kind StreamKind) (byte, error) {
	switch kind {
	case StreamRX:
		return 0x01, nil
	case StreamTX:
		return 0x02, nil
	default:
		return 0, errors.New("unsupported data stream kind")
	}
}

func ValidateStreamReply(kind StreamKind, reply byte) error {
	expected, err := ExpectedStreamReply(kind)
	if err != nil {
		return err
	}
	if reply != expected {
		return ErrGatewayRejected
	}
	return nil
}

type RXWorker struct {
	connection io.ReadCloser
	endpoint   PacketEndpoint
	decoder    *IPv4Decoder
	closeOnce  sync.Once
}

// AuthenticatedStreamOpenFunc opens an authenticated gateway data stream and
// returns its validated one-byte handshake reply.
type AuthenticatedStreamOpenFunc func(context.Context, StreamKind) (io.ReadWriteCloser, byte, error)
type HeartbeatBuilder func() ([]byte, error)

type DataStreamFactory struct {
	open       AuthenticatedStreamOpenFunc
	endpoint   PacketEndpoint
	identity   CommandIdentity
	heartbeat  HeartbeatBuilder
	maxPending int
	interval   time.Duration
}

func NewDataStreamFactory(open AuthenticatedStreamOpenFunc, endpoint PacketEndpoint, identity CommandIdentity, heartbeat HeartbeatBuilder, maxPending int, heartbeatInterval time.Duration) (*DataStreamFactory, error) {
	if open == nil || endpoint == nil || heartbeat == nil {
		return nil, errors.New("authenticated stream opener, endpoint, and heartbeat builder are required")
	}
	if !identity.AssignedIPv4.Is4() || !identity.HeartbeatLAN.Is4() {
		return nil, errors.New("data streams require command-channel IPv4 identity")
	}
	return &DataStreamFactory{open: open, endpoint: endpoint, identity: identity, heartbeat: heartbeat, maxPending: maxPending, interval: heartbeatInterval}, nil
}

func (factory *DataStreamFactory) Open(ctx context.Context, kind StreamKind) (StreamWorker, error) {
	connection, reply, err := factory.open(ctx, kind)
	if err != nil {
		return nil, err
	}
	if connection == nil {
		return nil, &TransportFailure{Code: FailureProtocolInvalid}
	}
	if err := ValidateStreamReply(kind, reply); err != nil {
		_ = connection.Close()
		return nil, err
	}
	switch kind {
	case StreamRX:
		worker, err := NewRXWorker(connection, factory.endpoint, factory.maxPending)
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		return worker, nil
	case StreamTX:
		heartbeat, err := factory.heartbeat()
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		worker, err := NewTXWorker(connection, factory.endpoint, heartbeat, factory.interval)
		clear(heartbeat)
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		return worker, nil
	default:
		_ = connection.Close()
		return nil, errors.New("unsupported data stream kind")
	}
}

func NewRXWorker(connection io.ReadCloser, endpoint PacketEndpoint, maxPending int) (*RXWorker, error) {
	if connection == nil || endpoint == nil {
		return nil, errors.New("RX connection and endpoint are required")
	}
	decoder, err := NewIPv4Decoder(maxPending)
	if err != nil {
		return nil, err
	}
	return &RXWorker{connection: connection, endpoint: endpoint, decoder: decoder}, nil
}

func (worker *RXWorker) Run(ctx context.Context) error {
	runContext, cancel := context.WithCancel(ctx)
	monitorDone := closeOnCancel(runContext, worker.connection)
	defer func() {
		cancel()
		worker.decoder.Reset()
		<-monitorDone
	}()
	buffer := make([]byte, 32*1024)
	for {
		count, err := worker.connection.Read(buffer)
		if count > 0 {
			if decodeErr := worker.decoder.Feed(buffer[:count], worker.endpoint.InjectInbound); decodeErr != nil {
				return decodeErr
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
}

func (worker *RXWorker) Close() error {
	var err error
	worker.closeOnce.Do(func() { err = worker.connection.Close() })
	return err
}

type TXWorker struct {
	connection        io.WriteCloser
	endpoint          PacketEndpoint
	heartbeat         []byte
	heartbeatInterval time.Duration
	closeOnce         sync.Once
}

func NewTXWorker(connection io.WriteCloser, endpoint PacketEndpoint, heartbeat []byte, interval time.Duration) (*TXWorker, error) {
	if connection == nil || endpoint == nil {
		return nil, errors.New("TX connection and endpoint are required")
	}
	if totalLength, err := validateIPv4Packet(heartbeat); err != nil || totalLength != 76 || len(heartbeat) != 76 || heartbeat[9] != 1 {
		return nil, errors.New("TX heartbeat must be a 76-byte IPv4 ICMP packet")
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &TXWorker{connection: connection, endpoint: endpoint, heartbeat: append([]byte(nil), heartbeat...), heartbeatInterval: interval}, nil
}

func (worker *TXWorker) Run(ctx context.Context) error {
	defer clear(worker.heartbeat)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	monitorDone := closeOnCancel(runContext, worker.connection)
	defer func() { <-monitorDone }()
	type packetResult struct {
		packet []byte
		err    error
	}
	packets := make(chan packetResult)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			packet, err := worker.endpoint.ReadOutbound(runContext)
			select {
			case packets <- packetResult{packet: packet, err: err}:
			case <-runContext.Done():
				clear(packet)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-readerDone
	}()
	ticker := time.NewTicker(worker.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-packets:
			if result.err != nil {
				return result.err
			}
			if totalLength, err := validateIPv4Packet(result.packet); err != nil || totalLength != len(result.packet) {
				clear(result.packet)
				return ErrInvalidIPv4Packet
			}
			err := writeFull(worker.connection, result.packet)
			clear(result.packet)
			if err != nil {
				return err
			}
		case <-ticker.C:
			if err := writeFull(worker.connection, worker.heartbeat); err != nil {
				return err
			}
		}
	}
}

func (worker *TXWorker) Close() error {
	var err error
	worker.closeOnce.Do(func() {
		err = worker.connection.Close()
	})
	return err
}

func BuildICMPHeartbeat(source, destination netip.Addr, token sessiontoken.NativeGatewayToken) ([]byte, error) {
	if !source.Is4() || !destination.Is4() {
		return nil, errors.New("heartbeat requires IPv4 source and destination")
	}
	if len(token) != agentTokenSize {
		return nil, errors.New("heartbeat requires a 48-byte agent token")
	}
	packet := make([]byte, 76)
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[4:6], []byte{0xbb, 0xaa})
	packet[8] = 64
	packet[9] = 1
	copy(packet[12:16], source.AsSlice())
	copy(packet[16:20], destination.AsSlice())
	packet[20] = 8
	copy(packet[24:28], []byte{0x55, 0x55, 0x44, 0x33})
	copy(packet[28:46], []byte("SANGFORSCSIPCLIENT"))
	fieldEnd := sessiontoken.NativeGatewaySessionFieldOffset + sessiontoken.NativeGatewaySessionFieldSize
	copy(packet[46:62], token[sessiontoken.NativeGatewaySessionFieldOffset:fieldEnd])
	copy(packet[62:75], []byte("L3VPNABCDEFGH"))
	binary.BigEndian.PutUint16(packet[22:24], checksum(packet[20:]))
	binary.BigEndian.PutUint16(packet[10:12], checksum(packet[:20]))
	return packet, nil
}

func checksum(data []byte) uint16 {
	var sum uint32
	for len(data) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(data[:2]))
		data = data[2:]
	}
	if len(data) == 1 {
		sum += uint32(data[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func closeOnCancel(ctx context.Context, connection io.Closer) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		_ = connection.Close()
	}()
	return done
}
