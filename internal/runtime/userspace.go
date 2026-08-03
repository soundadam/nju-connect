package runtime

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/sagernet/gvisor/pkg/buffer"
	"github.com/sagernet/gvisor/pkg/tcpip"
	"github.com/sagernet/gvisor/pkg/tcpip/adapters/gonet"
	"github.com/sagernet/gvisor/pkg/tcpip/header"
	"github.com/sagernet/gvisor/pkg/tcpip/link/channel"
	"github.com/sagernet/gvisor/pkg/tcpip/network/ipv4"
	"github.com/sagernet/gvisor/pkg/tcpip/stack"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/tcp"
	"github.com/sagernet/gvisor/pkg/tcpip/transport/udp"
)

const (
	userspaceNIC       = tcpip.NICID(1)
	userspaceQueueSize = 256
)

// Userspace owns a gVisor IPv4 TCP/UDP stack backed only by a channel
// endpoint. It creates no kernel interface and has no host route or DNS API.
type Userspace struct {
	stack     *stack.Stack
	link      *channel.Endpoint
	assigned  netip.Addr
	closed    atomic.Bool
	closeOnce sync.Once
}

func NewUserspace(assigned netip.Addr) (*Userspace, error) {
	if !assigned.Is4() {
		return nil, errors.New("userspace stack requires an assigned IPv4 address")
	}
	networkStack := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	linkEndpoint := channel.New(userspaceQueueSize, UserspaceMTU, "")
	if err := networkStack.CreateNIC(userspaceNIC, linkEndpoint); err != nil {
		networkStack.Destroy()
		return nil, errors.New("create userspace network interface")
	}
	protocolAddress := tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFrom4(assigned.As4()),
			PrefixLen: 32,
		},
	}
	if err := networkStack.AddProtocolAddress(userspaceNIC, protocolAddress, stack.AddressProperties{}); err != nil {
		networkStack.Destroy()
		return nil, errors.New("assign userspace IPv4 address")
	}
	networkStack.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: userspaceNIC}})
	return &Userspace{stack: networkStack, link: linkEndpoint, assigned: assigned}, nil
}

func (userspace *Userspace) AssignedIPv4() netip.Addr { return userspace.assigned }
func (userspace *Userspace) MTU() uint32              { return UserspaceMTU }

func (userspace *Userspace) InjectInbound(packet []byte) error {
	if userspace.closed.Load() {
		return &TransportFailure{Code: FailureRuntimeStopped}
	}
	totalLength, err := validateIPv4Packet(packet)
	if err != nil || totalLength != len(packet) {
		return ErrInvalidIPv4Packet
	}
	owned := append([]byte(nil), packet...)
	packetBuffer := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(owned)})
	defer packetBuffer.DecRef()
	userspace.link.InjectInbound(ipv4.ProtocolNumber, packetBuffer)
	return nil
}

func (userspace *Userspace) ReadOutbound(ctx context.Context) ([]byte, error) {
	if userspace.closed.Load() {
		return nil, &TransportFailure{Code: FailureRuntimeStopped}
	}
	packetBuffer := userspace.link.ReadContext(ctx)
	if packetBuffer == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, &TransportFailure{Code: FailureRuntimeStopped}
	}
	defer packetBuffer.DecRef()
	view := packetBuffer.ToView()
	defer view.Release()
	packet := append([]byte(nil), view.AsSlice()...)
	if totalLength, err := validateIPv4Packet(packet); err != nil || totalLength != len(packet) {
		clear(packet)
		return nil, ErrInvalidIPv4Packet
	}
	return packet, nil
}

func (userspace *Userspace) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if userspace.closed.Load() {
		return nil, &TransportFailure{Code: FailureRuntimeStopped}
	}
	if network != "tcp" && network != "tcp4" {
		return nil, errors.New("userspace ingress supports TCP only")
	}
	destination, err := netip.ParseAddrPort(address)
	if err != nil || !destination.Addr().Is4() {
		return nil, errors.New("userspace TCP destination must be numeric IPv4")
	}
	connection, err := gonet.DialContextTCP(ctx, userspace.stack, tcpip.FullAddress{
		NIC:  userspaceNIC,
		Addr: tcpip.AddrFrom4(destination.Addr().As4()),
		Port: destination.Port(),
	}, ipv4.ProtocolNumber)
	if err != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	return connection, nil
}

func (userspace *Userspace) Close() error {
	if userspace == nil {
		return nil
	}
	userspace.closeOnce.Do(func() {
		userspace.closed.Store(true)
		userspace.stack.Destroy()
	})
	return nil
}
