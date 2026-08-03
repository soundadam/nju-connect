package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestEasyConnect767SelectedProfileDrivesCommandAndDataWire(t *testing.T) {
	token := make([]byte, agentTokenSize)
	for index := range token {
		token[index] = byte(index + 1)
	}
	assigned := [4]byte{10, 1, 2, 3}
	lan := [4]byte{10, 9, 8, 7}
	var dialMu sync.Mutex
	dials := 0
	peerResults := make(chan error, 3)
	rawDial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		dialMu.Lock()
		generation := dials
		dials++
		dialMu.Unlock()
		go func() {
			peerResults <- serveEasyConnect767IntegrationPeer(server, generation, token, assigned, lan)
		}()
		return client, nil
	}
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{Dial: rawDial})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("fixture complete")
	heartbeatWaits := 0
	var identity CommandIdentity
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Profile:           profile,
		Token:             token,
		HeartbeatInterval: time.Nanosecond,
		HeartbeatDeadline: func(context.Context) time.Time { return time.Now().Add(time.Hour) },
		Wait: func(_ context.Context, duration time.Duration) error {
			if duration == time.Nanosecond {
				heartbeatWaits++
				if heartbeatWaits == 1 {
					return nil
				}
			}
			return stop
		},
		OnIdentity: func(got CommandIdentity) error {
			identity = got
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(context.Background(), func(Component, bool) {}); !errors.Is(err, stop) {
		t.Fatalf("command supervisor error = %v", err)
	}
	if identity.AssignedIPv4 != netip.AddrFrom4(assigned) || identity.HeartbeatLAN != netip.AddrFrom4(lan) {
		t.Fatalf("command identity = %+v", identity)
	}
	opener, err := NewAuthenticatedDataStreamOpener(profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []StreamKind{StreamRX, StreamTX} {
		connection, reply, err := opener.Open(context.Background(), kind, token, identity.AssignedIPv4)
		if err != nil {
			t.Fatalf("open stream %#x: %v", byte(kind), err)
		}
		expected, _ := ExpectedStreamReply(kind)
		if reply != expected {
			t.Fatalf("stream %#x reply = %#x", byte(kind), reply)
		}
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if err := <-peerResults; err != nil {
			t.Fatal(err)
		}
	}
	dialMu.Lock()
	defer dialMu.Unlock()
	if dials != 3 {
		t.Fatalf("profile dials = %d", dials)
	}
}

func serveEasyConnect767IntegrationPeer(connection net.Conn, generation int, token []byte, assigned, lan [4]byte) error {
	defer connection.Close()
	preface := make([]byte, len(easyConnect767ClientPreface))
	if _, err := io.ReadFull(connection, preface); err != nil {
		return fmt.Errorf("read preface: %w", err)
	}
	if !bytes.Equal(preface, []byte(easyConnect767ClientPreface)) {
		return errors.New("fixed client preface mismatch")
	}
	serverFlight := bytes.Repeat([]byte{0xa5}, easyConnect767ServerFlightSize)
	if err := writeFull(connection, serverFlight); err != nil {
		return fmt.Errorf("write server flight: %w", err)
	}
	final := make([]byte, len(easyConnect767ClientFinal))
	if _, err := io.ReadFull(connection, final); err != nil {
		return fmt.Errorf("read client final: %w", err)
	}
	if !bytes.Equal(final, []byte(easyConnect767ClientFinal)) {
		return errors.New("fixed client final mismatch")
	}
	switch generation {
	case 0:
		return serveEasyConnect767CommandFixture(connection, token, assigned, lan)
	case 1:
		return serveEasyConnect767DataFixture(connection, StreamRX, token, assigned)
	case 2:
		return serveEasyConnect767DataFixture(connection, StreamTX, token, assigned)
	default:
		return errors.New("unexpected profile dial")
	}
}

func serveEasyConnect767CommandFixture(connection net.Conn, token []byte, assigned, lan [4]byte) error {
	initial := make([]byte, easyConnect767InitialFrameSize)
	if _, err := io.ReadFull(connection, initial); err != nil {
		return fmt.Errorf("read op0 frame: %w", err)
	}
	if !bytes.Equal(initial[:12], []byte(easyConnect767InitialFrameHeader)) {
		return errors.New("op0 frame header mismatch")
	}
	payload := initial[12:]
	if binary.LittleEndian.Uint32(payload[:4]) != 0 || !bytes.Equal(payload[4:52], token) || binary.LittleEndian.Uint32(payload[60:64]) != 0xffffffff {
		return errors.New("op0 payload mismatch")
	}
	reply := make([]byte, commandReplySize)
	copy(reply[4:8], assigned[:])
	copy(reply[12:16], lan[:])
	if err := writeEasyConnect767IntegrationReply(connection, reply); err != nil {
		return err
	}
	established := make([]byte, easyConnect767EstablishedFrameSize)
	if _, err := io.ReadFull(connection, established); err != nil {
		return fmt.Errorf("read op3 frame: %w", err)
	}
	if !bytes.Equal(established[:4], []byte(easyConnect767EstablishedFrameHeader)) {
		return errors.New("op3 frame header mismatch")
	}
	payload = established[4:]
	if binary.LittleEndian.Uint32(payload[:4]) != 3 || !bytes.Equal(payload[4:52], token) {
		return errors.New("op3 payload mismatch")
	}
	binary.LittleEndian.PutUint32(reply[:4], 15)
	if err := writeEasyConnect767IntegrationReply(connection, reply); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, connection)
	return nil
}

func serveEasyConnect767DataFixture(connection net.Conn, kind StreamKind, token []byte, assigned [4]byte) error {
	frame := make([]byte, easyConnect767InitialFrameSize)
	if _, err := io.ReadFull(connection, frame); err != nil {
		return fmt.Errorf("read data frame: %w", err)
	}
	if !bytes.Equal(frame[:12], []byte(easyConnect767InitialFrameHeader)) {
		return errors.New("data frame header mismatch")
	}
	payload := frame[12:]
	if binary.LittleEndian.Uint32(payload[:4]) != uint32(kind) || !bytes.Equal(payload[4:52], token) {
		return errors.New("data payload mismatch")
	}
	if !bytes.Equal(payload[60:64], []byte{assigned[3], assigned[2], assigned[1], assigned[0]}) {
		return errors.New("data assigned address mismatch")
	}
	reply := make([]byte, commandReplySize)
	expected, _ := ExpectedStreamReply(kind)
	binary.LittleEndian.PutUint32(reply[:4], uint32(expected))
	if err := writeEasyConnect767IntegrationReply(connection, reply); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, connection)
	return nil
}

func writeEasyConnect767IntegrationReply(writer io.Writer, payload []byte) error {
	frame := append([]byte(easyConnect767ReplyFrameHeader), payload...)
	defer clear(frame)
	return writeFull(writer, frame)
}
