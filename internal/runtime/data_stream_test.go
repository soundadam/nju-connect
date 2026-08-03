package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingConn struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
	closes    int
}

func (connection *recordingConn) SetDeadline(deadline time.Time) error {
	connection.mu.Lock()
	connection.deadlines = append(connection.deadlines, deadline)
	connection.mu.Unlock()
	return connection.Conn.SetDeadline(deadline)
}

func (connection *recordingConn) Close() error {
	connection.mu.Lock()
	connection.closes++
	connection.mu.Unlock()
	return connection.Conn.Close()
}

func (connection *recordingConn) snapshot() ([]time.Time, int) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	return append([]time.Time(nil), connection.deadlines...), connection.closes
}

func TestAuthenticatedDataStreamWireLayout(t *testing.T) {
	for _, test := range []struct {
		kind  StreamKind
		reply byte
	}{{StreamRX, 0x01}, {StreamTX, 0x02}} {
		t.Run(string(rune(test.kind)), func(t *testing.T) {
			client, server := net.Pipe()
			recorded := &recordingConn{Conn: client}
			request := make(chan []byte, 1)
			go func() {
				defer server.Close()
				message := make([]byte, commandRequestSize)
				if _, err := io.ReadFull(server, message); err != nil {
					return
				}
				request <- message
				_, _ = server.Write([]byte{test.reply})
				_, _ = io.Copy(io.Discard, server)
			}()
			opener, err := NewAuthenticatedDataStreamOpener(func(context.Context) (net.Conn, error) { return recorded, nil })
			if err != nil {
				t.Fatal(err)
			}
			fixedNow := time.Now()
			opener.now = func() time.Time { return fixedNow }
			connection, reply, err := opener.Open(context.Background(), test.kind, testAgentToken(), netip.MustParseAddr("10.1.2.3"))
			if err != nil {
				t.Fatal(err)
			}
			if reply != test.reply {
				t.Fatalf("reply = %#x", reply)
			}
			message := <-request
			if message[0] != byte(test.kind) || !bytes.Equal(message[1:4], make([]byte, 3)) {
				t.Fatalf("stream prefix = %x", message[:4])
			}
			if !bytes.Equal(message[4:52], testAgentToken()) || !bytes.Equal(message[52:60], make([]byte, 8)) {
				t.Fatal("stream token or reserved bytes mismatch")
			}
			if !bytes.Equal(message[60:64], []byte{3, 2, 1, 10}) {
				t.Fatalf("reversed assigned address = %x", message[60:64])
			}
			deadlines, _ := recorded.snapshot()
			if len(deadlines) != 2 || !deadlines[0].Equal(fixedNow.Add(15*time.Second)) || !deadlines[1].IsZero() {
				t.Fatalf("deadlines = %v", deadlines)
			}
			_ = connection.Close()
		})
	}
}

func TestAuthenticatedDataStreamRejectsWithoutExposingReply(t *testing.T) {
	client, server := net.Pipe()
	recorded := &recordingConn{Conn: client}
	go func() {
		defer server.Close()
		message := make([]byte, commandRequestSize)
		_, _ = io.ReadFull(server, message)
		_, _ = server.Write([]byte{0xe7})
	}()
	opener, err := NewAuthenticatedDataStreamOpener(func(context.Context) (net.Conn, error) { return recorded, nil })
	if err != nil {
		t.Fatal(err)
	}
	connection, reply, err := opener.Open(context.Background(), StreamRX, testAgentToken(), netip.MustParseAddr("10.0.0.2"))
	if connection != nil || reply != 0 {
		t.Fatalf("rejected connection=%v reply=%#x", connection, reply)
	}
	var renewal *RenewalRequired
	if !errors.As(err, &renewal) || renewal.Reason != RenewalGatewayRejected {
		t.Fatalf("rejection error = %v", err)
	}
	if strings.Contains(err.Error(), "e7") {
		t.Fatalf("rejection exposed reply: %v", err)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("rejected data stream was not closed")
	}
}

func TestAuthenticatedDataStreamCancellationClosesPendingConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	requestRead := make(chan struct{})
	go func() {
		message := make([]byte, commandRequestSize)
		_, _ = io.ReadFull(server, message)
		close(requestRead)
		_, _ = io.Copy(io.Discard, server)
	}()
	opener, err := NewAuthenticatedDataStreamOpener(func(context.Context) (net.Conn, error) { return recorded, nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, err := opener.Open(ctx, StreamTX, testAgentToken(), netip.MustParseAddr("10.0.0.2"))
		result <- err
	}()
	<-requestRead
	cancel()
	select {
	case err := <-result:
		var failure *TransportFailure
		if !errors.As(err, &failure) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled data stream did not return")
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("canceled data stream was not closed")
	}
}

func TestAuthenticatedDataStreamHidesRawDialFailure(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	opener, err := NewAuthenticatedDataStreamOpener(func(context.Context) (net.Conn, error) {
		return recorded, errors.New("secret gateway reply and token")
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = opener.Open(context.Background(), StreamRX, testAgentToken(), netip.MustParseAddr("10.0.0.2"))
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("dial failure = %v", err)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("connection returned with dial failure was not closed")
	}
}
