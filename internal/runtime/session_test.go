package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/core"
)

func TestNativeSessionOwnsAndJoinsCompleteRuntime(t *testing.T) {
	token := make([]byte, agentTokenSize)
	for index := range token {
		token[index] = byte(index + 1)
	}
	expectedToken := append([]byte(nil), token...)
	var commandTokensMu sync.Mutex
	var commandTokens [][]byte
	commandDial := func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		go serveCommand(t, server, expectedToken, [4]byte{10, 0, 0, 2}, [4]byte{10, 0, 0, 1}, false, &commandTokensMu, &commandTokens)
		return client, nil
	}
	var peersMu sync.Mutex
	var peers []net.Conn
	openData := func(_ context.Context, kind StreamKind, borrowedToken []byte, assigned netip.Addr) (io.ReadWriteCloser, byte, error) {
		if !bytes.Equal(borrowedToken, expectedToken) {
			return nil, 0, errors.New("borrowed token changed")
		}
		if assigned != netip.MustParseAddr("10.0.0.2") {
			return nil, 0, errors.New("assigned address changed")
		}
		client, peer := net.Pipe()
		peersMu.Lock()
		peers = append(peers, peer)
		peersMu.Unlock()
		reply, err := ExpectedStreamReply(kind)
		return client, reply, err
	}
	connected := make(chan struct{}, 1)
	listening := make(chan struct{}, 1)
	session, err := NewNativeSession(NativeSessionConfig{
		Plan: core.DataplanePlan{
			Mode:               core.DataplaneL3VPN,
			LocalAgentRequired: true,
			BoundaryReady:      true,
		},
		AgentToken:       token,
		CommandDial:      commandDial,
		OpenDataStream:   openData,
		SOCKSBind:        "127.0.0.1:0",
		CommandHeartbeat: time.Hour,
		DataHeartbeat:    time.Hour,
		OnState: func(state State) {
			if state == StateConnected {
				select {
				case connected <- struct{}{}:
				default:
				}
			}
		},
		OnListen: func(net.Addr) {
			select {
			case listening <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	clear(token)
	result := make(chan error, 1)
	go func() { result <- session.Run(context.Background()) }()
	waitSignal(t, listening, "native SOCKS listener")
	waitSignal(t, connected, "native connected state")
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("native session error = %v", err)
	}
	peersMu.Lock()
	for _, peer := range peers {
		_ = peer.Close()
	}
	peerCount := len(peers)
	peersMu.Unlock()
	if peerCount != 2 {
		t.Fatalf("opened data streams = %d", peerCount)
	}
	commandTokensMu.Lock()
	defer commandTokensMu.Unlock()
	if len(commandTokens) != 1 || !bytes.Equal(commandTokens[0], expectedToken) {
		t.Fatalf("command token generations = %d", len(commandTokens))
	}
}

func TestNativeSessionRequiresReadyL3VPNPlan(t *testing.T) {
	_, err := NewNativeSession(NativeSessionConfig{Plan: core.DataplanePlan{Mode: core.DataplaneNone}})
	if err == nil {
		t.Fatal("non-L3VPN plan was accepted")
	}
}

func TestNativeSessionCloseBeforeRunPreventsLaterStart(t *testing.T) {
	token := make([]byte, agentTokenSize)
	session, err := NewNativeSession(NativeSessionConfig{
		Plan: core.DataplanePlan{
			Mode:               core.DataplaneL3VPN,
			LocalAgentRequired: true,
			BoundaryReady:      true,
		},
		AgentToken: token,
		CommandDial: func(context.Context) (net.Conn, error) {
			return nil, errors.New("unused")
		},
		SOCKSBind: "localhost:1080",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Run(context.Background()); err == nil {
		t.Fatal("closed native session was started")
	}
}
