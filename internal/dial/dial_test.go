package dial

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSplitServerAddsDefaultPort(t *testing.T) {
	host, address, err := SplitServer("vpn.example.edu")
	if err != nil {
		t.Fatal(err)
	}
	if host != "vpn.example.edu" || address != "vpn.example.edu:443" {
		t.Fatalf("SplitServer() = %q, %q", host, address)
	}
}

func TestProbeUpstreamDistinguishesListenerAvailability(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := ProbeUpstream(context.Background(), "socks5://"+address); err != nil {
		t.Fatalf("ready listener probe = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ProbeUpstream(ctx, "socks5://"+address); !errors.Is(err, ErrUpstreamUnavailable) {
		t.Fatalf("closed listener probe = %v", err)
	}
}

func TestNewRejectsProxyCredentialURL(t *testing.T) {
	if _, err := New("socks5://user:password@127.0.0.1:2081", time.Second); err == nil {
		t.Fatal("credential-bearing proxy URL unexpectedly accepted")
	}
}
