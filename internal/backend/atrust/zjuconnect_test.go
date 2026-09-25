package atrustbackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	upstreamclient "github.com/mythologyli/zju-connect/client"
	upstreamresolve "github.com/mythologyli/zju-connect/resolve"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/runtime"
)

type fakeUpstream struct {
	ipResources     []upstreamclient.IPResource
	domainResources map[string]upstreamclient.DomainResource
	dnsResources    map[string]net.IP
	dialed          chan context.Context
	closed          int
}

func newFakeUpstream() *fakeUpstream {
	return &fakeUpstream{
		ipResources: []upstreamclient.IPResource{{
			IPMin: net.ParseIP("172.21.0.0"), IPMax: net.ParseIP("172.21.255.255"),
			PortMin: 0, PortMax: 65535, Protocol: "all",
		}},
		domainResources: map[string]upstreamclient.DomainResource{
			".example.edu.cn": {PortMin: 1, PortMax: 65535, Protocol: "tcp", AppID: "wildcard"},
			"bad name.edu.cn": {PortMin: 1, PortMax: 65535, Protocol: "tcp"},
		},
		dnsResources: map[string]net.IP{"lib.example.edu.cn": net.ParseIP("172.21.0.9")},
		dialed:       make(chan context.Context, 1),
	}
}

func (upstream *fakeUpstream) IPResources() ([]upstreamclient.IPResource, error) {
	return upstream.ipResources, nil
}

func (upstream *fakeUpstream) DomainResources() (map[string]upstreamclient.DomainResource, error) {
	return upstream.domainResources, nil
}

func (upstream *fakeUpstream) DNSResource() (map[string]net.IP, error) {
	return upstream.dnsResources, nil
}

func (upstream *fakeUpstream) DialTCP(ctx context.Context, _ *net.TCPAddr) (net.Conn, error) {
	upstream.dialed <- ctx
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (upstream *fakeUpstream) Close() { upstream.closed++ }

// scanPrompt behaves like the upstream client: it logs a prompt through the
// standard logger and reads the answer with fmt.Scanln from os.Stdin.
func scanPrompt(prompt string) (string, error) {
	log.Print(prompt + ": ")
	var answer string
	_, err := fmt.Scanln(&answer)
	return answer, err
}

func quietLogger(t *testing.T) {
	t.Helper()
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
}

func passwordLogin() LoginRequest {
	return LoginRequest{
		Endpoint: backend.ATrustEndpoint("vpn.nju.edu.cn", 443),
		Method:   backend.AuthenticationMethod{Type: passwordAuthType, Domain: "openldap13924"},
		Username: "student",
	}
}

func TestZJUCoreBridgesSMSPromptToPrompter(t *testing.T) {
	quietLogger(t)
	originalStdin := os.Stdin
	upstream := newFakeUpstream()
	var seen setupRequest
	core := zjuCore{setup: func(request setupRequest) (upstreamClient, []byte, error) {
		seen = request
		code, err := scanPrompt(upstreamSMSPrompt)
		if err != nil || code != "123456" {
			return nil, nil, fmt.Errorf("code=%q err=%v", code, err)
		}
		return upstream, []byte(`{"cookies":"synthetic"}`), nil
	}}
	prompter := PrompterFuncs{
		OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("synthetic-password"), nil },
		OnVerificationCode: func(_ context.Context, request VerificationRequest) ([]byte, error) {
			if request.Channel != "sms" {
				t.Errorf("channel = %q", request.Channel)
			}
			return []byte("123456"), nil
		},
	}

	session, err := core.Authenticate(context.Background(), passwordLogin(), prompter)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if os.Stdin != originalStdin {
		t.Fatal("standard input was not restored")
	}
	if seen.Server != backend.DefaultATrustGatewayAddress || seen.Port != 443 || seen.Password != "synthetic-password" ||
		seen.LoginDomain != "openldap13924" || seen.AuthType != passwordAuthType || seen.GraphCodeFile == "" {
		t.Fatalf("setup request = %+v", seen)
	}
	if _, err := os.Stat(seen.GraphCodeFile); !os.IsNotExist(err) {
		t.Fatalf("captcha workspace remains: %v", err)
	}
	clientData, err := session.ClientData()
	if err != nil || string(clientData) != `{"cookies":"synthetic"}` {
		t.Fatalf("client data = %q, %v", clientData, err)
	}
}

func TestZJUCoreReportsUnavailableFactor(t *testing.T) {
	quietLogger(t)
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		if _, err := scanPrompt(upstreamSMSPrompt); err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("upstream accepted an empty answer")
	}}
	prompter := PrompterFuncs{OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }}
	if _, err := core.Authenticate(context.Background(), passwordLogin(), prompter); !errors.Is(err, ErrFactorUnavailable) {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestZJUCoreReportsRejectedPassword(t *testing.T) {
	quietLogger(t)
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		log.Println("Perform POST /passport/v1/auth/psw")
		log.Printf("Code: %d, Message: %s", 10302, "用户名或密码错误")
		log.Println("Perform POST /passport/v1/auth/authCheck")
		log.Printf("Code: %d, Message: %s", 1, "unrelated")
		return nil, nil, errors.New("unsupported next authentication service: auth/unknown")
	}}
	prompter := PrompterFuncs{OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("wrong"), nil }}
	_, err := core.Authenticate(context.Background(), passwordLogin(), prompter)
	if !errors.Is(err, backend.ErrCredentialRejected) {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if want := "gateway rejected the username or password (gateway code 10302: 用户名或密码错误)"; err.Error() != want {
		t.Fatalf("Authenticate() error = %q, want %q", err, want)
	}
}

func TestZJUCoreIgnoresAnswersOutsidePasswordLogin(t *testing.T) {
	quietLogger(t)
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		log.Println("Perform POST /passport/v1/auth/psw")
		log.Printf("Code: %d, Message: %s", 0, "")
		log.Println("Perform POST /passport/v1/auth/sms")
		log.Printf("Code: %d, Message: %s", 10501, "wrong code")
		return nil, nil, errors.New("sms rejected")
	}}
	prompter := PrompterFuncs{OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil }}
	_, err := core.Authenticate(context.Background(), passwordLogin(), prompter)
	if err == nil || errors.Is(err, backend.ErrCredentialRejected) {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

func TestZJUCorePassesOAuthCodeUpFront(t *testing.T) {
	quietLogger(t)
	var seen setupRequest
	core := zjuCore{setup: func(request setupRequest) (upstreamClient, []byte, error) {
		seen = request
		return newFakeUpstream(), nil, nil
	}}
	login := passwordLogin()
	login.Method = backend.AuthenticationMethod{Type: oauthAuthType, Domain: "tenant", LoginURL: "https://vpn.nju.edu.cn/login"}
	prompter := PrompterFuncs{OnOAuthCode: func(_ context.Context, request OAuthRequest) (string, error) {
		if request.LoginURL != "https://vpn.nju.edu.cn/login" || request.Endpoint.Host != "vpn.nju.edu.cn" {
			t.Errorf("OAuth request = %+v", request)
		}
		return "synthetic-code", nil
	}}
	session, err := core.Authenticate(context.Background(), login, prompter)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if seen.OAuth2Code != "synthetic-code" || seen.Password != "" || seen.AuthType != oauthAuthType {
		t.Fatalf("setup request = %+v", seen)
	}
}

func TestZJUCoreResumeMapsRejectionToExpired(t *testing.T) {
	quietLogger(t)
	endpoint := backend.ATrustEndpoint("vpn.nju.edu.cn", 443)
	for _, test := range []struct {
		name    string
		setup   setupFunc
		expired bool
	}{
		{name: "rejected", expired: true, setup: func(setupRequest) (upstreamClient, []byte, error) {
			return nil, nil, errors.New("login failed")
		}},
		{name: "prompted", expired: true, setup: func(setupRequest) (upstreamClient, []byte, error) {
			_, err := scanPrompt(upstreamSMSPrompt)
			return nil, nil, err
		}},
		{name: "network", expired: false, setup: func(setupRequest) (upstreamClient, []byte, error) {
			return nil, nil, &net.OpError{Op: "dial", Err: errors.New("unreachable")}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := zjuCore{setup: test.setup}.Resume(context.Background(), ResumeRequest{Endpoint: endpoint, ClientData: []byte("saved")})
			if err == nil || errors.Is(err, ErrSessionExpired) != test.expired {
				t.Fatalf("Resume() error = %v", err)
			}
		})
	}
}

func TestZJUCoreCancelsPendingPrompt(t *testing.T) {
	quietLogger(t)
	originalStdin := os.Stdin
	setupReturned := make(chan struct{})
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		defer close(setupReturned)
		_, err := scanPrompt(upstreamSMSPrompt)
		return nil, nil, err
	}}
	ctx, cancel := context.WithCancel(context.Background())
	prompter := PrompterFuncs{
		OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil },
		OnVerificationCode: func(ctx context.Context, _ VerificationRequest) ([]byte, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	if _, err := core.Authenticate(ctx, passwordLogin(), prompter); !errors.Is(err, context.Canceled) {
		t.Fatalf("Authenticate() error = %v", err)
	}
	select {
	case <-setupReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream read was not released")
	}
	// The bridge lock is released once Setup returns; a new bridge must be
	// installable, and standard input must be restored afterwards.
	bridge, err := installStdioBridge(context.Background(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	bridge.Close()
	if os.Stdin != originalStdin {
		t.Fatal("standard input was not restored")
	}
}

func TestTranslateResourcesDropsUnrepresentableRules(t *testing.T) {
	upstream := newFakeUpstream()
	resources := translateResources(upstream.ipResources, upstream.domainResources, upstream.dnsResources)
	if err := resources.Validate(); err != nil {
		t.Fatal(err)
	}
	want := Resources{
		IPRules: []IPRule{{
			Range:    IPRange{First: netip.MustParseAddr("172.21.0.0"), Last: netip.MustParseAddr("172.21.255.255")},
			Ports:    AllPorts(),
			Protocol: ProtocolAll,
		}},
		DomainRules:  []DomainRule{{Domain: "example.edu.cn", IncludeSubdomains: true, Ports: AllPorts(), Protocol: ProtocolTCP}},
		DNSOverrides: []DNSOverride{{Domain: "lib.example.edu.cn", Addresses: []netip.Addr{netip.MustParseAddr("172.21.0.9")}}},
	}
	if !reflect.DeepEqual(resources, want) {
		t.Fatalf("resources = %+v", resources)
	}
	if !resources.MatchIP(netip.MustParseAddr("172.21.0.1"), 443, ProtocolTCP) {
		t.Fatal("gateway resource 172.21.0.1 is not routed")
	}
}

func TestZJUTunnelTagsDomainResources(t *testing.T) {
	upstream := newFakeUpstream()
	session, err := newZJUSession(upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	tunnel, _ := session.OpenTunnel(context.Background())
	destination := &net.TCPAddr{IP: net.ParseIP("172.21.0.9"), Port: 443}

	ctx := runtime.WithSOCKSDomain(context.Background(), "Lib.Example.edu.cn.")
	conn, err := tunnel.DialTCP(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	dialed := <-upstream.dialed
	if host, _ := dialed.Value(upstreamresolve.ContextKeyResolveHost).(string); host != "lib.example.edu.cn" {
		t.Fatalf("resolve host = %q", host)
	}
	if resource, _ := dialed.Value(upstreamresolve.ContextKeyDomainResource).(upstreamclient.DomainResource); resource.AppID != "wildcard" {
		t.Fatalf("domain resource = %+v", resource)
	}

	conn, err = tunnel.DialTCP(runtime.WithSOCKSDomain(context.Background(), "notexample.edu.cn"), destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if (<-upstream.dialed).Value(upstreamresolve.ContextKeyDomainResource) != nil {
		t.Fatal("dot boundary ignored")
	}

	_ = session.Close()
	_ = session.Close()
	if upstream.closed != 1 {
		t.Fatalf("upstream closed %d times", upstream.closed)
	}
	if _, err := session.ClientData(); err == nil {
		t.Fatal("closed session returned client data")
	}
}
