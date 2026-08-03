package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/traffic"
)

func TestSOCKSTCPRelayCountsOnlyApplicationPayload(t *testing.T) {
	var counters traffic.Counters
	counters.BeginSession(time.Unix(1000, 0))
	dialed := make(chan string, 1)
	dialer := TCPDialFunc(func(_ context.Context, network, address string) (net.Conn, error) {
		if network != "tcp4" {
			t.Errorf("network = %q", network)
		}
		dialed <- address
		client, remote := net.Pipe()
		go func() {
			defer remote.Close()
			payload := make([]byte, 4)
			if _, err := io.ReadFull(remote, payload); err == nil && string(payload) == "ping" {
				_, _ = remote.Write([]byte("pong"))
			}
		}()
		return client, nil
	})
	server, err := NewSOCKSServer(SOCKSConfig{Bind: "127.0.0.1:0", Dialer: dialer, Counters: &counters})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	ready := make(chan struct{}, 1)
	go func() {
		result <- server.Run(ctx, func(component Component, available bool) {
			if component == ComponentSOCKS && available {
				ready <- struct{}{}
			}
		})
	}()
	<-ready
	connection, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFull(connection, []byte{5, 2, 0, 2}); err != nil {
		t.Fatal(err)
	}
	method := make([]byte, 2)
	if _, err := io.ReadFull(connection, method); err != nil || string(method) != string([]byte{5, 0}) {
		t.Fatalf("method reply = %v, %v", method, err)
	}
	request := []byte{5, 1, 0, 1, 1, 2, 3, 4, 0, 0}
	binary.BigEndian.PutUint16(request[8:10], 443)
	if err := writeFull(connection, request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(connection, reply); err != nil || reply[1] != 0 {
		t.Fatalf("CONNECT reply = %v, %v", reply, err)
	}
	if got := <-dialed; got != "1.2.3.4:443" {
		t.Fatalf("destination = %q", got)
	}
	if err := writeFull(connection, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 4)
	if _, err := io.ReadFull(connection, payload); err != nil || string(payload) != "pong" {
		t.Fatalf("payload = %q, %v", payload, err)
	}
	_ = connection.Close()
	eventuallyTraffic(t, &counters, 4, 4, 0)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("server error = %v", err)
	}
	snapshot := counters.Snapshot()
	if snapshot.TotalConnections != 1 {
		t.Fatalf("traffic snapshot = %+v", snapshot)
	}
}

func TestSOCKSRejectsNonLoopbackBindAndExcessConcurrency(t *testing.T) {
	dialer := TCPDialFunc(func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") })
	for _, config := range []SOCKSConfig{
		{Bind: "0.0.0.0:1080", Dialer: dialer},
		{Bind: "127.0.0.1:1080", Dialer: dialer, MaxConnections: 257},
	} {
		if _, err := NewSOCKSServer(config); err == nil {
			t.Fatalf("config %+v was accepted", config)
		}
	}
}

func eventuallyTraffic(t *testing.T, counters *traffic.Counters, upload, download uint64, active int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot := counters.Snapshot()
		if snapshot.UploadBytes == upload && snapshot.DownloadBytes == download && snapshot.ActiveConnections == active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("traffic snapshot = %+v", counters.Snapshot())
}
