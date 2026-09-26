package atrustbackend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	upstreamclient "github.com/mythologyli/zju-connect/client"
	upstream "github.com/mythologyli/zju-connect/client/atrust"
	upstreamresolve "github.com/mythologyli/zju-connect/resolve"
	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/runtime"
)

const (
	passwordAuthType = "auth/psw"
	oauthAuthType    = "auth/httpsOauth2"

	// updateBestNodesInterval is how often, in seconds, the upstream client
	// re-probes tunnel nodes while a session runs.
	updateBestNodesInterval = 300
)

// NewCore returns the aTrust protocol core linked into this build: an
// adapter over the pinned AGPL-3.0 github.com/mythologyli/zju-connect
// implementation.
//
// The upstream client deviates from the Core contract in two documented
// ways. It dials the gateway and tunnel nodes with its own interface-bound
// dialer, so LoginRequest.Dial and ResumeRequest.Dial are ignored; and it
// does not verify gateway or node certificates. It also reads interactive
// factors from standard input; the adapter redirects those reads to the
// Prompter (see stdioBridge), so no terminal input reaches it.
func NewCore() Core { return zjuCore{setup: setupUpstream} }

// upstreamClient is the part of the upstream client the adapter uses.
type upstreamClient interface {
	IPResources() ([]upstreamclient.IPResource, error)
	DomainResources() (map[string]upstreamclient.DomainResource, error)
	DNSResource() (map[string]net.IP, error)
	DialTCP(context.Context, *net.TCPAddr) (net.Conn, error)
	Close()
}

// setupRequest carries the arguments of one upstream Client.Setup call.
type setupRequest struct {
	Server        string
	Port          int
	Username      string
	Password      string
	LoginDomain   string
	AuthType      string
	GraphCodeFile string
	OAuth2Code    string
	ClientData    []byte
}

// setupFunc runs an upstream login and returns the connected client and its
// opaque client data. Tests replace it.
type setupFunc func(setupRequest) (upstreamClient, []byte, error)

func setupUpstream(request setupRequest) (upstreamClient, []byte, error) {
	client := upstream.NewClient("", "", "", "")
	clientData, err := client.Setup(
		request.Server,
		request.Port,
		request.Username,
		request.Password,
		"",
		request.LoginDomain,
		request.AuthType,
		request.GraphCodeFile,
		"",
		request.OAuth2Code,
		request.ClientData,
		nil,
		updateBestNodesInterval,
		"",
		false,
	)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, clientData, nil
}

type zjuCore struct {
	setup setupFunc
}

func (core zjuCore) Discover(ctx context.Context, endpoint backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	type result struct {
		methods []backend.AuthenticationMethod
		err     error
	}
	completed := make(chan result, 1)
	go func() {
		info, err := upstream.GetAuthInfoList(endpoint.DialHost(), endpoint.Port, "", false)
		methods := make([]backend.AuthenticationMethod, 0, len(info))
		for _, method := range info {
			methods = append(methods, backend.AuthenticationMethod{
				Domain:   method.LoginDomain,
				Type:     method.AuthType,
				Name:     method.AuthName,
				LoginURL: method.LoginURL,
			})
		}
		completed <- result{methods: methods, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case outcome := <-completed:
		if outcome.err != nil {
			var networkError net.Error
			if errors.As(outcome.err, &networkError) {
				return nil, errors.New("aTrust discovery failed")
			}
			return nil, outcome.err
		}
		return outcome.methods, nil
	}
}

func (core zjuCore) Authenticate(ctx context.Context, request LoginRequest, prompter Prompter) (Session, error) {
	if err := validateEndpoint(request.Endpoint); err != nil {
		return nil, err
	}
	if prompter == nil {
		return nil, errors.New("aTrust login requires an interactive prompter")
	}
	if request.Method.Domain == "" {
		return nil, errors.New("aTrust login domain is required")
	}
	setup := setupRequest{
		Server:      request.Endpoint.DialHost(),
		Port:        request.Endpoint.Port,
		Username:    request.Username,
		LoginDomain: request.Method.Domain,
		AuthType:    request.Method.Type,
	}
	switch request.Method.Type {
	case passwordAuthType:
		password, err := prompter.Password(ctx, PasswordRequest{Username: request.Username, LoginDomain: request.Method.Domain})
		if err != nil {
			return nil, err
		}
		// The upstream API takes the password as a string; the byte copy is
		// cleared here and the string is dropped with the request.
		setup.Password = string(password)
		credential.Clear(password)
	case oauthAuthType:
		code, err := prompter.OAuthCode(ctx, OAuthRequest{Endpoint: request.Endpoint, LoginURL: request.Method.LoginURL})
		if err != nil {
			return nil, err
		}
		if code == "" {
			return nil, errors.New("aTrust OAuth authorization code is empty")
		}
		setup.OAuth2Code = code
	default:
		return nil, fmt.Errorf("aTrust authentication type %q is not supported", request.Method.Type)
	}
	return core.run(ctx, setup, prompter)
}

func (core zjuCore) Resume(ctx context.Context, request ResumeRequest) (Session, error) {
	if err := validateEndpoint(request.Endpoint); err != nil {
		return nil, err
	}
	if len(request.ClientData) == 0 {
		return nil, ErrSessionExpired
	}
	// An empty auth type makes the upstream client log in with the saved
	// cookies only. Any factor it asks for is refused by the nil prompter.
	session, err := core.run(ctx, setupRequest{
		Server:     request.Endpoint.DialHost(),
		Port:       request.Endpoint.Port,
		ClientData: append([]byte(nil), request.ClientData...),
	}, nil)
	if err == nil {
		return session, nil
	}
	var networkError net.Error
	if ctx.Err() != nil || errors.Is(err, ErrSessionExpired) || errors.As(err, &networkError) {
		return nil, err
	}
	return nil, fmt.Errorf("%w: %v", ErrSessionExpired, err)
}

// run performs one upstream Setup with standard input bridged to prompter.
// The bridge stays installed until Setup returns, even when ctx ends first,
// so a late upstream read never reaches the real terminal.
func (core zjuCore) run(ctx context.Context, request setupRequest, prompter Prompter) (Session, error) {
	setup := core.setup
	if setup == nil {
		setup = setupUpstream
	}
	captchaDirectory, err := os.MkdirTemp("", "nju-connect-atrust-")
	if err != nil {
		return nil, errors.New("aTrust captcha workspace is unavailable")
	}
	request.GraphCodeFile = filepath.Join(captchaDirectory, "captcha.png")

	bridge, err := installStdioBridge(ctx, prompter, request.GraphCodeFile)
	if err != nil {
		_ = os.RemoveAll(captchaDirectory)
		return nil, err
	}
	type result struct {
		client     upstreamClient
		clientData []byte
		err        error
	}
	completed := make(chan result, 1)
	go func() {
		client, clientData, setupErr := setup(request)
		credential.Clear(request.ClientData)
		bridge.Close()
		_ = os.RemoveAll(captchaDirectory)
		completed <- result{client: client, clientData: clientData, err: setupErr}
	}()

	select {
	case <-ctx.Done():
		go func() {
			if late := <-completed; late.client != nil {
				late.client.Close()
				credential.Clear(late.clientData)
			}
		}()
		return nil, ctx.Err()
	case outcome := <-completed:
		if outcome.err != nil {
			if promptErr := bridge.Err(); promptErr != nil {
				return nil, promptErr
			}
			if rejection := bridge.Rejection(); rejection != "" {
				return nil, fmt.Errorf("%w (%s)", backend.ErrCredentialRejected, rejection)
			}
			return nil, fmt.Errorf("aTrust login failed: %w", outcome.err)
		}
		session, err := newZJUSession(outcome.client, outcome.clientData)
		credential.Clear(outcome.clientData)
		if err != nil {
			outcome.client.Close()
			return nil, err
		}
		return session, nil
	}
}

type zjuSession struct {
	client          upstreamClient
	clientData      []byte
	ipResources     []upstreamclient.IPResource
	domainResources map[string]upstreamclient.DomainResource
	dnsResources    map[string]net.IP

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func newZJUSession(client upstreamClient, clientData []byte) (*zjuSession, error) {
	ipResources, err := client.IPResources()
	if err != nil {
		return nil, errors.New("aTrust IP resources are unavailable")
	}
	domainResources, err := client.DomainResources()
	if err != nil {
		return nil, errors.New("aTrust domain resources are unavailable")
	}
	dnsResources, err := client.DNSResource()
	if err != nil {
		return nil, errors.New("aTrust DNS resources are unavailable")
	}
	return &zjuSession{
		client:          client,
		clientData:      append([]byte(nil), clientData...),
		ipResources:     ipResources,
		domainResources: domainResources,
		dnsResources:    dnsResources,
	}, nil
}

func (session *zjuSession) ClientData() ([]byte, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil, errors.New("aTrust session is closed")
	}
	return append([]byte(nil), session.clientData...), nil
}

func (session *zjuSession) Resources(context.Context) (Resources, error) {
	return translateResources(session.ipResources, session.domainResources, session.dnsResources), nil
}

func (session *zjuSession) OpenTunnel(context.Context) (Tunnel, error) {
	return &zjuTunnel{session: session, done: make(chan struct{})}, nil
}

// Logout is local only: the pinned upstream client has no gateway logout,
// and the caller clears the saved client data.
func (session *zjuSession) Logout(context.Context) error { return nil }

func (session *zjuSession) Close() error {
	session.closeOnce.Do(func() {
		session.mu.Lock()
		session.closed = true
		credential.Clear(session.clientData)
		session.clientData = nil
		session.mu.Unlock()
		session.client.Close()
	})
	return nil
}

// zjuTunnel dials through the upstream client, which maintains its own node
// selection and keepalive for the lifetime of the session.
type zjuTunnel struct {
	session   *zjuSession
	done      chan struct{}
	closeOnce sync.Once
}

func (tunnel *zjuTunnel) DialTCP(ctx context.Context, destination *net.TCPAddr) (net.Conn, error) {
	if destination == nil || destination.IP.To4() == nil {
		return nil, errors.New("aTrust tunnel supports only IPv4 TCP destinations")
	}
	target := &net.TCPAddr{IP: destination.IP.To16(), Port: destination.Port}
	if domain, ok := runtime.SOCKSDomain(ctx); ok {
		if resource, found := matchDomainResource(domain, destination.Port, tunnel.session.domainResources); found {
			// The upstream client picks the resource's application and node
			// group from these keys and asks the node for the name, not the
			// locally resolved address.
			ctx = context.WithValue(ctx, upstreamresolve.ContextKeyResolveHost, normalizeDomain(domain))
			ctx = context.WithValue(ctx, upstreamresolve.ContextKeyDomainResource, resource)
		}
	}
	return tunnel.session.client.DialTCP(ctx, target)
}

func (tunnel *zjuTunnel) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-tunnel.done:
		return errors.New("aTrust tunnel closed")
	}
}

func (tunnel *zjuTunnel) Close() error {
	tunnel.closeOnce.Do(func() { close(tunnel.done) })
	return nil
}

// matchDomainResource returns the most specific upstream domain resource for
// host. The upstream parser turns a wildcard such as "*.example.edu.cn" into
// ".example.edu.cn"; a dot boundary is required so "notexample.edu.cn"
// cannot inherit the resource.
func matchDomainResource(host string, port int, resources map[string]upstreamclient.DomainResource) (upstreamclient.DomainResource, bool) {
	host = normalizeDomain(host)
	var matched upstreamclient.DomainResource
	matchedLength := -1
	for domain, resource := range resources {
		domain = strings.TrimPrefix(normalizeDomain(domain), ".")
		if domain == "" {
			continue
		}
		hostMatches := host == domain || strings.HasSuffix(host, "."+domain)
		if hostMatches && resource.PortMin <= port && port <= resource.PortMax &&
			(resource.Protocol == "tcp" || resource.Protocol == "all") && len(domain) > matchedLength {
			matched = resource
			matchedLength = len(domain)
		}
	}
	return matched, matchedLength >= 0
}

// translateResources converts upstream resources into nju-connect's model.
// Entries nju-connect cannot represent are dropped individually so one
// malformed gateway rule never disables routing for the rest.
func translateResources(
	ipResources []upstreamclient.IPResource,
	domainResources map[string]upstreamclient.DomainResource,
	dnsResources map[string]net.IP,
) Resources {
	var resources Resources
	for _, resource := range ipResources {
		first, firstOK := netip.AddrFromSlice(resource.IPMin)
		last, lastOK := netip.AddrFromSlice(resource.IPMax)
		ports, portsOK := translatePorts(resource.PortMin, resource.PortMax)
		if !firstOK || !lastOK || !portsOK {
			continue
		}
		rule := IPRule{
			Range:    IPRange{First: first.Unmap(), Last: last.Unmap()},
			Ports:    ports,
			Protocol: Protocol(resource.Protocol),
		}
		if rule.Range.validate() == nil && validateRuleShape(rule.Ports, rule.Protocol) == nil {
			resources.IPRules = append(resources.IPRules, rule)
		}
	}
	for name, resource := range domainResources {
		ports, portsOK := translatePorts(resource.PortMin, resource.PortMax)
		if !portsOK {
			continue
		}
		// Every upstream domain resource also covers its subdomains, matching
		// matchDomainResource and the upstream resolver.
		rule := DomainRule{
			Domain:            strings.TrimPrefix(normalizeDomain(name), "."),
			IncludeSubdomains: true,
			Ports:             ports,
			Protocol:          Protocol(resource.Protocol),
		}
		if validateDomain(rule.Domain) == nil && validateRuleShape(rule.Ports, rule.Protocol) == nil {
			resources.DomainRules = append(resources.DomainRules, rule)
		}
	}
	for name, ip := range dnsResources {
		address, ok := netip.AddrFromSlice(ip)
		override := DNSOverride{Domain: normalizeDomain(name), Addresses: []netip.Addr{address.Unmap()}}
		if ok && validateDomain(override.Domain) == nil {
			resources.DNSOverrides = append(resources.DNSOverrides, override)
		}
	}
	slices.SortFunc(resources.DomainRules, func(a, b DomainRule) int { return strings.Compare(a.Domain, b.Domain) })
	slices.SortFunc(resources.DNSOverrides, func(a, b DNSOverride) int { return strings.Compare(a.Domain, b.Domain) })
	return resources
}

func translatePorts(first, last int) ([]PortRange, bool) {
	first = max(first, 1)
	last = min(last, 65535)
	if last < first {
		return nil, false
	}
	return []PortRange{{First: uint16(first), Last: uint16(last)}}, true
}

var _ Core = zjuCore{}
