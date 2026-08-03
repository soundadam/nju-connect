package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
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

func TestCommandSupervisorBoundsInitialEstablishmentAndReportsSafeStages(t *testing.T) {
	const attemptLimit = 3
	const sensitive = "secret proxy node, token, and gateway reply"
	fixedNow := time.Unix(1234, 0)
	var dials int
	var delays []time.Duration
	var failures []CommandFailure
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial: func(context.Context) (net.Conn, error) {
			dials++
			return nil, errors.New(sensitive)
		},
		Token:               make([]byte, agentTokenSize),
		InitialAttemptLimit: attemptLimit,
		Now:                 func() time.Time { return fixedNow },
		Wait: func(_ context.Context, duration time.Duration) error {
			delays = append(delays, duration)
			return nil
		},
		OnFailure: func(failure CommandFailure) {
			failures = append(failures, failure)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = supervisor.Run(context.Background(), func(Component, bool) {})
	var transport *TransportFailure
	if !errors.As(err, &transport) || transport.Code != FailureTransportUnavailable {
		t.Fatalf("initial establishment error = %v", err)
	}
	if dials != attemptLimit {
		t.Fatalf("dial attempts = %d, want %d", dials, attemptLimit)
	}
	wantDelays := []time.Duration{4 * time.Second, 8 * time.Second}
	if !slices.Equal(delays, wantDelays) {
		t.Fatalf("backoff delays = %v, want %v", delays, wantDelays)
	}
	if len(failures) != attemptLimit {
		t.Fatalf("command failures = %v", failures)
	}
	for index, failure := range failures {
		if failure.Attempt != uint64(index+1) || failure.Stage != StageUpstreamConnectFailed || !failure.At.Equal(fixedNow) {
			t.Fatalf("command failure %d = %+v", index, failure)
		}
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("initial establishment exposed a raw error: %v", err)
	}
}

func TestCommandSupervisorReportsFixedSendIPStages(t *testing.T) {
	tests := []struct {
		name  string
		stage FailureStage
		dial  CommandDialer
	}{
		{
			name:  "write failed",
			stage: StageSendIPWriteFailed,
			dial: func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				_ = server.Close()
				return client, nil
			},
		},
		{
			name:  "read failed",
			stage: StageSendIPReadFailed,
			dial: func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					request := make([]byte, commandRequestSize)
					_, _ = io.ReadFull(server, request)
					clear(request)
				}()
				return client, nil
			},
		},
		{
			name:  "rejected",
			stage: StageSendIPRejected,
			dial: func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					request := make([]byte, commandRequestSize)
					_, _ = io.ReadFull(server, request)
					clear(request)
					reply := make([]byte, commandReplySize)
					binary.LittleEndian.PutUint32(reply[:4], 9)
					_, _ = server.Write(reply)
					clear(reply)
				}()
				return client, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixedNow := time.Unix(2345, 0)
			var failures []CommandFailure
			waited := false
			supervisor, err := NewCommandSupervisor(CommandConfig{
				Dial:                test.dial,
				Token:               make([]byte, agentTokenSize),
				InitialAttemptLimit: 1,
				Now:                 func() time.Time { return fixedNow },
				Wait: func(context.Context, time.Duration) error {
					waited = true
					return nil
				},
				OnFailure: func(failure CommandFailure) {
					failures = append(failures, failure)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			err = supervisor.Run(context.Background(), func(Component, bool) {})
			if test.stage == StageSendIPRejected {
				var renewal *RenewalRequired
				if !errors.As(err, &renewal) || renewal.Reason != RenewalGatewayRejected {
					t.Fatalf("rejection error = %v", err)
				}
			} else {
				var transport *TransportFailure
				if !errors.As(err, &transport) || transport.Code != FailureTransportUnavailable {
					t.Fatalf("establishment error = %v", err)
				}
			}
			if waited {
				t.Fatal("final initial failure scheduled another retry")
			}
			if len(failures) != 1 || failures[0].Attempt != 1 || failures[0].Stage != test.stage || !failures[0].At.Equal(fixedNow) {
				t.Fatalf("command failures = %v", failures)
			}
		})
	}
}

func TestCommandInitialAttemptTimeoutClosesPendingSendIPRead(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	requestRead := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		request := make([]byte, commandRequestSize)
		_, _ = io.ReadFull(server, request)
		clear(request)
		close(requestRead)
		_, _ = io.Copy(io.Discard, server)
	}()
	var failures []CommandFailure
	fixedNow := time.Unix(3456, 0)
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial:                func(context.Context) (net.Conn, error) { return recorded, nil },
		Token:               make([]byte, agentTokenSize),
		AttemptTimeout:      20 * time.Millisecond,
		InitialAttemptLimit: 1,
		Now:                 func() time.Time { return fixedNow },
		OnFailure: func(failure CommandFailure) {
			failures = append(failures, failure)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = supervisor.Run(context.Background(), func(Component, bool) {})
	<-requestRead
	<-serverDone
	var transport *TransportFailure
	if !errors.As(err, &transport) || transport.Code != FailureTransportUnavailable {
		t.Fatalf("attempt timeout error = %v", err)
	}
	if len(failures) != 1 || failures[0].Stage != StageSendIPReadFailed || !failures[0].At.Equal(fixedNow) {
		t.Fatalf("command failures = %v", failures)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("timed-out SEND_IP connection was not closed")
	}
}

func TestCommandSupervisorStableWindowStartsAfterConnect(t *testing.T) {
	token := make([]byte, agentTokenSize)
	stop := errors.New("stop test")
	now := time.Unix(1000, 0)
	dials := 0
	var delays []time.Duration
	supervisor, err := NewCommandSupervisor(CommandConfig{
		Dial: func(context.Context) (net.Conn, error) {
			dials++
			if dials == 1 {
				return nil, errors.New("temporary transport failure")
			}
			now = now.Add(2 * time.Minute)
			client, server := net.Pipe()
			go serveCommand(t, server, token, [4]byte{10, 0, 0, 2}, [4]byte{10, 0, 0, 1}, true, nil, nil)
			return client, nil
		},
		Token:             token,
		HeartbeatInterval: time.Millisecond,
		StableFor:         time.Minute,
		Now:               func() time.Time { return now },
		Wait: func(_ context.Context, duration time.Duration) error {
			if duration == time.Millisecond {
				return nil
			}
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

func TestCommandSupervisorHandlesInvalidDialResults(t *testing.T) {
	t.Run("connection with error is closed", func(t *testing.T) {
		client, server := net.Pipe()
		defer server.Close()
		recorded := &recordingConn{Conn: client}
		stop := errors.New("stop test")
		supervisor, err := NewCommandSupervisor(CommandConfig{
			Dial:  func(context.Context) (net.Conn, error) { return recorded, errors.New("dial failed") },
			Token: make([]byte, agentTokenSize),
			Wait:  func(context.Context, time.Duration) error { return stop },
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := supervisor.Run(context.Background(), func(Component, bool) {}); !errors.Is(err, stop) {
			t.Fatalf("run error = %v", err)
		}
		_, closes := recorded.snapshot()
		if closes == 0 {
			t.Fatal("connection returned with dial error was not closed")
		}
	})

	t.Run("nil connection is transient", func(t *testing.T) {
		stop := errors.New("stop test")
		supervisor, err := NewCommandSupervisor(CommandConfig{
			Dial:  func(context.Context) (net.Conn, error) { return nil, nil },
			Token: make([]byte, agentTokenSize),
			Wait:  func(context.Context, time.Duration) error { return stop },
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := supervisor.Run(context.Background(), func(Component, bool) {}); !errors.Is(err, stop) {
			t.Fatalf("run error = %v", err)
		}
	})
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
