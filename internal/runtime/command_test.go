package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestCommandSupervisorReconnectsWithSameToken(t *testing.T) {
	token := make([]byte, agentTokenSize)
	for index := range token {
		token[index] = byte(index + 1)
	}
	var mu sync.Mutex
	var received [][]byte
	dials := 0
	secondReady := make(chan struct{})
	dial := func(ctx context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		dials++
		generation := dials
		go serveCommand(t, server, token, [4]byte{10, 0, 0, 2}, [4]byte{10, 0, 0, 1}, generation == 1, &mu, &received)
		return client, nil
	}
	waits := make(chan time.Duration, 4)
	ctx, cancel := context.WithCancel(context.Background())
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial: dial, Token: token, HeartbeatInterval: time.Millisecond,
		Wait: func(ctx context.Context, duration time.Duration) error {
			waits <- duration
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- supervisor.Run(ctx, func(component Component, ready bool) {
			if component == ComponentCommand && ready && dials == 2 {
				select {
				case <-secondReady:
				default:
					close(secondReady)
				}
			}
		})
	}()
	select {
	case <-secondReady:
	case <-time.After(time.Second):
		t.Fatal("command channel did not reconnect")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) < 2 {
		t.Fatalf("received %d SEND_IP tokens", len(received))
	}
	for _, got := range received[:2] {
		if string(got) != string(token) {
			t.Fatal("reconnect did not use the original token")
		}
	}
}

func TestCommandSupervisorRequiresStableAddresses(t *testing.T) {
	token := make([]byte, agentTokenSize)
	dials := 0
	dial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		dials++
		assigned := [4]byte{10, 0, 0, byte(dials + 1)}
		go serveCommand(t, server, token, assigned, [4]byte{10, 0, 0, 1}, true, nil, nil)
		return client, nil
	}
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial: dial, Token: token, HeartbeatInterval: time.Millisecond,
		Wait: func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	err = supervisor.Run(context.Background(), func(Component, bool) {})
	var renewal *RenewalRequired
	if !errors.As(err, &renewal) || renewal.Reason != RenewalAddressChanged {
		t.Fatalf("error = %#v", err)
	}
}

func TestCommandSupervisorUsesBoundedExponentialBackoff(t *testing.T) {
	token := make([]byte, agentTokenSize)
	stop := errors.New("stop test")
	var delays []time.Duration
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial: func(context.Context) (net.Conn, error) {
			return nil, errors.New("temporary transport failure")
		},
		Token: token,
		Wait: func(_ context.Context, duration time.Duration) error {
			delays = append(delays, duration)
			if len(delays) == 2 {
				return stop
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(context.Background(), func(Component, bool) {}); !errors.Is(err, stop) {
		t.Fatalf("run error = %v", err)
	}
	want := []time.Duration{4 * time.Second, 8 * time.Second}
	if len(delays) != len(want) || delays[0] != want[0] || delays[1] != want[1] {
		t.Fatalf("backoff delays = %v, want %v", delays, want)
	}
}

func serveCommand(t *testing.T, connection net.Conn, token []byte, assigned, lan [4]byte, closeHeartbeat bool, mu *sync.Mutex, received *[][]byte) {
	t.Helper()
	defer connection.Close()
	request := make([]byte, commandRequestSize)
	if _, err := io.ReadFull(connection, request); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(request[:4]) != 0 || binary.LittleEndian.Uint32(request[60:]) != 0xffffffff {
		t.Errorf("invalid SEND_IP frame")
		return
	}
	if !bytes.Equal(request[4:52], token) {
		t.Error("SEND_IP token mismatch")
		return
	}
	if !bytes.Equal(request[52:60], make([]byte, 8)) {
		t.Error("SEND_IP reserved bytes mismatch")
		return
	}
	if received != nil {
		copyToken := append([]byte(nil), request[4:52]...)
		mu.Lock()
		*received = append(*received, copyToken)
		mu.Unlock()
	}
	reply := make([]byte, commandReplySize)
	copy(reply[4:8], assigned[:])
	copy(reply[12:16], lan[:])
	if err := writeFull(connection, reply); err != nil {
		return
	}
	if _, err := io.ReadFull(connection, request); err != nil {
		return
	}
	if binary.LittleEndian.Uint32(request[:4]) != 3 {
		t.Errorf("heartbeat op = %d, want 3", binary.LittleEndian.Uint32(request[:4]))
		return
	}
	if !bytes.Equal(request[4:52], token) {
		t.Error("heartbeat token mismatch")
		return
	}
	if !bytes.Equal(request[52:64], make([]byte, 12)) {
		t.Error("heartbeat reserved bytes mismatch")
		return
	}
	if closeHeartbeat {
		return
	}
	binary.LittleEndian.PutUint32(reply[:4], 15)
	if err := writeFull(connection, reply); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, connection)
}
