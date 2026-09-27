package atrustbackend

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/soundadam/nju-connect/internal/runtime"
)

const defaultDirectDialTimeout = 10 * time.Second

// TunnelDialer is the part of Tunnel the router needs.
type TunnelDialer interface {
	DialTCP(ctx context.Context, destination *net.TCPAddr) (net.Conn, error)
}

// Resolver resolves names that have no gateway DNS override.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Route names where the router sends a connection.
type Route string

const (
	RouteTunnel Route = "tunnel"
	RouteDirect Route = "direct"
)

// RouterConfig wires resources to the tunnel and the host's direct path.
type RouterConfig struct {
	Resources Resources
	Tunnel    TunnelDialer
	// Direct carries destinations that are not aTrust resources. It defaults
	// to the system dialer so non-resource traffic never reaches the gateway.
	Direct runtime.TCPDialer
	// Resolver answers names without a DNS override. It defaults to the
	// system resolver.
	Resolver Resolver
}

// Router is the SOCKS dialer and resolver for an aTrust session. A
// destination uses the tunnel when either its original SOCKS domain or its
// numeric address matches a resource rule; everything else goes direct.
type Router struct {
	resources Resources
	tunnel    TunnelDialer
	direct    runtime.TCPDialer
	resolver  Resolver
}

func NewRouter(config RouterConfig) (*Router, error) {
	if config.Tunnel == nil {
		return nil, errors.New("aTrust tunnel is required")
	}
	if err := config.Resources.Validate(); err != nil {
		return nil, err
	}
	if config.Direct == nil {
		config.Direct = &net.Dialer{Timeout: defaultDirectDialTimeout}
	}
	if config.Resolver == nil {
		config.Resolver = net.DefaultResolver
	}
	return &Router{
		resources: config.Resources,
		tunnel:    config.Tunnel,
		direct:    config.Direct,
		resolver:  config.Resolver,
	}, nil
}

// Route decides the path for a numeric TCP destination. domain is the name
// the client originally requested, or empty.
func (router *Router) Route(domain string, destination netip.AddrPort) Route {
	if domain != "" && router.resources.MatchDomain(domain, destination.Port(), ProtocolTCP) {
		return RouteTunnel
	}
	if router.resources.MatchIP(destination.Addr(), destination.Port(), ProtocolTCP) {
		return RouteTunnel
	}
	return RouteDirect
}

// DialContext implements runtime.TCPDialer for the shared SOCKS server.
func (router *Router) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return nil, errors.New("aTrust router supports TCP only")
	}
	destination, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, errors.New("aTrust router requires a numeric destination")
	}
	destination = netip.AddrPortFrom(destination.Addr().Unmap(), destination.Port())
	domain, _ := runtime.SOCKSDomain(ctx)
	if router.Route(domain, destination) == RouteTunnel {
		return router.tunnel.DialTCP(ctx, net.TCPAddrFromAddrPort(destination))
	}
	return router.direct.DialContext(ctx, network, destination.String())
}

// ResolveIPv4 implements runtime.ResolveIPv4Func: gateway DNS overrides win,
// then the fallback resolver.
func (router *Router) ResolveIPv4(ctx context.Context, name string) (netip.Addr, error) {
	if addresses, ok := router.resources.LookupDNSOverride(name); ok {
		for _, address := range addresses {
			if address.Is4() {
				return address, nil
			}
		}
		return netip.Addr{}, errors.New("DNS override has no IPv4 address")
	}
	if literal, err := netip.ParseAddr(name); err == nil {
		if literal.Unmap().Is4() {
			return literal.Unmap(), nil
		}
		return netip.Addr{}, errors.New("destination is not IPv4")
	}
	addresses, err := router.resolver.LookupNetIP(ctx, "ip4", name)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, address := range addresses {
		if address.Unmap().Is4() {
			return address.Unmap(), nil
		}
	}
	return netip.Addr{}, errors.New("no IPv4 address for " + strconv.Quote(name))
}
