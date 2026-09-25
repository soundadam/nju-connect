package atrustbackend

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type recordingDialer struct {
	network string
	address string
}

func (dialer *recordingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	dialer.network, dialer.address = network, address
	local, remote := net.Pipe()
	_ = remote.Close()
	return local, nil
}

type recordingTunnel struct{ destination string }

func (tunnel *recordingTunnel) DialTCP(_ context.Context, destination *net.TCPAddr) (net.Conn, error) {
	tunnel.destination = destination.String()
	local, remote := net.Pipe()
	_ = remote.Close()
	return local, nil
}

type staticResolver map[string][]netip.Addr

func (resolver staticResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	if network != "ip4" {
		return nil, errors.New("unexpected network")
	}
	if addresses, ok := resolver[host]; ok {
		return addresses, nil
	}
	return nil, errors.New("not found")
}

func TestRouterSendsOnlyResourcesThroughTunnel(t *testing.T) {
	t.Parallel()
	tunnel := &recordingTunnel{}
	direct := &recordingDialer{}
	router, err := NewRouter(RouterConfig{Resources: testResources(), Tunnel: tunnel, Direct: direct})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := router.DialContext(context.Background(), "tcp4", "10.10.1.1:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if tunnel.destination != "10.10.1.1:443" || direct.address != "" {
		t.Fatalf("tunnel=%q direct=%q", tunnel.destination, direct.address)
	}

	tunnel.destination = ""
	connection, err = router.DialContext(context.Background(), "tcp4", "203.0.113.5:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if tunnel.destination != "" || direct.address != "203.0.113.5:443" {
		t.Fatalf("tunnel=%q direct=%q", tunnel.destination, direct.address)
	}

	if router.Route("git.intranet.example.edu", netip.MustParseAddrPort("203.0.113.5:443")) != RouteTunnel {
		t.Fatal("domain resource on a non-resource address did not use the tunnel")
	}
	if router.Route("git.intranet.example.edu", netip.MustParseAddrPort("203.0.113.5:80")) != RouteDirect {
		t.Fatal("domain resource on a disallowed port used the tunnel")
	}
	if _, err := router.DialContext(context.Background(), "udp", "10.10.1.1:53"); err == nil {
		t.Fatal("router accepted UDP")
	}
	if _, err := router.DialContext(context.Background(), "tcp4", "intranet.example.edu:443"); err == nil {
		t.Fatal("router accepted a non-numeric destination")
	}
}

func TestRouterResolvesOverridesBeforeFallback(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(RouterConfig{
		Resources: testResources(),
		Tunnel:    &recordingTunnel{},
		Resolver:  staticResolver{"public.example.org": {netip.MustParseAddr("198.51.100.7")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"git.intranet.example.edu": "10.10.1.2",
		"public.example.org":       "198.51.100.7",
		"192.0.2.44":               "192.0.2.44",
	} {
		got, err := router.ResolveIPv4(context.Background(), name)
		if err != nil || got.String() != want {
			t.Fatalf("ResolveIPv4(%q) = %v, %v; want %s", name, got, err, want)
		}
	}
	if _, err := router.ResolveIPv4(context.Background(), "missing.example.org"); err == nil {
		t.Fatal("ResolveIPv4 invented an address")
	}
}

func TestNewRouterRequiresTunnelAndValidResources(t *testing.T) {
	t.Parallel()
	if _, err := NewRouter(RouterConfig{}); err == nil {
		t.Fatal("NewRouter accepted a missing tunnel")
	}
	invalid := Resources{DomainRules: []DomainRule{{Domain: "*.example.edu", Protocol: ProtocolTCP}}}
	if _, err := NewRouter(RouterConfig{Resources: invalid, Tunnel: &recordingTunnel{}}); err == nil {
		t.Fatal("NewRouter accepted invalid resources")
	}
}

func TestPrompterFuncsReportUnavailableFactors(t *testing.T) {
	t.Parallel()
	var prompter Prompter = PrompterFuncs{}
	ctx := context.Background()
	if _, err := prompter.Password(ctx, PasswordRequest{}); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("Password() = %v", err)
	}
	if _, err := prompter.VerificationCode(ctx, VerificationRequest{}); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("VerificationCode() = %v", err)
	}
	if _, err := prompter.Captcha(ctx, CaptchaChallenge{}); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("Captcha() = %v", err)
	}
	if _, err := prompter.OAuthCode(ctx, OAuthRequest{}); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("OAuthCode() = %v", err)
	}
}
