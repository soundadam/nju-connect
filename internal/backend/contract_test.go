package backend

import "testing"

func TestParseName(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]Name{
		"easyconnect":   EasyConnect,
		" EasyConnect ": EasyConnect,
		"atrust":        ATrust,
		"aTrust":        ATrust,
	} {
		input, want := input, want
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseName(input)
			if err != nil || got != want {
				t.Fatalf("ParseName(%q) = %q, %v; want %q", input, got, err, want)
			}
		})
	}
	if _, err := ParseName("unknown"); err == nil {
		t.Fatal("ParseName accepted an unknown backend")
	}
}

func TestCatalogDescribesBothBackendsWithoutProtocolState(t *testing.T) {
	t.Parallel()
	descriptors := Catalog()
	if len(descriptors) != 2 {
		t.Fatalf("Catalog length = %d, want 2", len(descriptors))
	}
	if descriptors[0].ID != EasyConnect || descriptors[0].DefaultGateway != DefaultEasyConnectGateway {
		t.Fatalf("EasyConnect descriptor = %#v", descriptors[0])
	}
	if descriptors[1].ID != ATrust || descriptors[1].DefaultGateway != DefaultATrustGateway {
		t.Fatalf("aTrust descriptor = %#v", descriptors[1])
	}
	if descriptors[1].Authentication[0] != AuthenticationSharedPassword {
		t.Fatalf("aTrust authentication = %#v", descriptors[1].Authentication)
	}
}

func TestATrustEndpointPinsNJUGatewayAddress(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		host     string
		wantHost string
		wantDial string
	}{
		{host: "vpn.nju.edu.cn", wantHost: "vpn.nju.edu.cn", wantDial: DefaultATrustGatewayAddress},
		{host: "VPN.nju.edu.cn", wantHost: "VPN.nju.edu.cn", wantDial: DefaultATrustGatewayAddress},
		{host: "ztna.nju.edu.cn", wantHost: DefaultATrustGateway, wantDial: DefaultATrustGatewayAddress},
		{host: "gateway.example.edu", wantHost: "gateway.example.edu", wantDial: "gateway.example.edu"},
		{host: "219.219.118.20", wantHost: "219.219.118.20", wantDial: "219.219.118.20"},
	} {
		endpoint := ATrustEndpoint(test.host, 443)
		if endpoint.Host != test.wantHost || endpoint.DialHost() != test.wantDial || endpoint.Port != 443 {
			t.Fatalf("ATrustEndpoint(%q) = %#v (dial %q); want host %q dial %q",
				test.host, endpoint, endpoint.DialHost(), test.wantHost, test.wantDial)
		}
	}
}
