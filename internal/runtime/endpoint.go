package runtime

import (
	"context"
	"net"
)

const UserspaceMTU = 1400

// PacketEndpoint is the narrow boundary between the native L3VPN streams and
// a userspace IPv4 stack. The concrete netstack adapter owns the assigned /32
// and must not create a kernel TUN or alter host networking.
type PacketEndpoint interface {
	InjectInbound([]byte) error
	ReadOutbound(context.Context) ([]byte, error)
}

// TCPDialer is implemented by the userspace stack and consumed by SOCKS. It
// intentionally exposes TCP only during the first ingress stage.
type TCPDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
