package runtime

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestUserspaceEmitsRawIPv4WithoutHostNetworking(t *testing.T) {
	assigned := netip.MustParseAddr("10.20.30.40")
	userspace, err := NewUserspace(assigned)
	if err != nil {
		t.Fatal(err)
	}
	defer userspace.Close()
	if userspace.AssignedIPv4() != assigned || userspace.MTU() != 1400 {
		t.Fatalf("userspace address=%s mtu=%d", userspace.AssignedIPv4(), userspace.MTU())
	}
	ctx, cancel := context.WithCancel(context.Background())
	dialDone := make(chan error, 1)
	go func() {
		connection, err := userspace.DialContext(ctx, "tcp4", "192.0.2.10:443")
		if connection != nil {
			_ = connection.Close()
		}
		dialDone <- err
	}()
	readContext, stopRead := context.WithTimeout(context.Background(), time.Second)
	defer stopRead()
	packet, err := userspace.ReadOutbound(readContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) > UserspaceMTU || packet[0]>>4 != 4 || packet[9] != 6 {
		t.Fatalf("outbound packet length=%d header=%x", len(packet), packet[:20])
	}
	if source := netip.AddrFrom4([4]byte(packet[12:16])); source != assigned {
		t.Fatalf("outbound source = %s", source)
	}
	cancel()
	select {
	case <-dialDone:
	case <-time.After(time.Second):
		t.Fatal("userspace dial did not honor cancellation")
	}
}

func TestUserspaceRejectsInvalidInboundAndCloseUnblocksOutbound(t *testing.T) {
	userspace, err := NewUserspace(netip.MustParseAddr("10.0.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := userspace.InjectInbound(make([]byte, minimumIPv4Header)); !errors.Is(err, ErrInvalidIPv4Packet) {
		t.Fatalf("invalid inbound error = %v", err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := userspace.ReadOutbound(context.Background())
		result <- err
	}()
	if err := userspace.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		var failure *TransportFailure
		if !errors.As(err, &failure) || failure.Code != FailureRuntimeStopped {
			t.Fatalf("outbound close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("userspace close did not unblock outbound reader")
	}
}
