package atrustbackend

import (
	"net/netip"
	"testing"
)

func testResources() Resources {
	return Resources{
		IPRules: []IPRule{
			{Range: IPRangeFromPrefix(netip.MustParsePrefix("10.10.0.0/16")), Protocol: ProtocolAll},
			{
				Range:    IPRange{First: netip.MustParseAddr("172.16.0.10"), Last: netip.MustParseAddr("172.16.0.20")},
				Ports:    []PortRange{{First: 22, Last: 22}, {First: 8000, Last: 8099}},
				Protocol: ProtocolTCP,
			},
			{Range: IPRangeFromPrefix(netip.MustParsePrefix("192.0.2.0/24")), Protocol: ProtocolUDP},
		},
		DomainRules: []DomainRule{
			{Domain: "lib.example.edu", Protocol: ProtocolAll},
			{Domain: "intranet.example.edu", IncludeSubdomains: true, Ports: []PortRange{{First: 443, Last: 443}}, Protocol: ProtocolTCP},
		},
		DNSOverrides: []DNSOverride{
			{Domain: "git.intranet.example.edu", Addresses: []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("10.10.1.2")}},
		},
	}
}

func TestIPRangeFromPrefixCoversWholePrefix(t *testing.T) {
	t.Parallel()
	for prefix, want := range map[string]IPRange{
		"10.10.0.0/16":   {First: netip.MustParseAddr("10.10.0.0"), Last: netip.MustParseAddr("10.10.255.255")},
		"10.10.7.9/32":   {First: netip.MustParseAddr("10.10.7.9"), Last: netip.MustParseAddr("10.10.7.9")},
		"10.10.7.9/20":   {First: netip.MustParseAddr("10.10.0.0"), Last: netip.MustParseAddr("10.10.15.255")},
		"2001:db8::/126": {First: netip.MustParseAddr("2001:db8::"), Last: netip.MustParseAddr("2001:db8::3")},
	} {
		if got := IPRangeFromPrefix(netip.MustParsePrefix(prefix)); got != want {
			t.Fatalf("IPRangeFromPrefix(%s) = %v, want %v", prefix, got, want)
		}
	}
}

func TestResourcesMatchIPHonorsRangePortAndProtocol(t *testing.T) {
	t.Parallel()
	resources := testResources()
	if err := resources.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		address  string
		port     uint16
		protocol Protocol
		want     bool
	}{
		{"10.10.3.4", 443, ProtocolTCP, true},
		{"10.11.0.1", 443, ProtocolTCP, false},
		{"172.16.0.15", 22, ProtocolTCP, true},
		{"172.16.0.15", 8050, ProtocolTCP, true},
		{"172.16.0.15", 443, ProtocolTCP, false},
		{"172.16.0.21", 22, ProtocolTCP, false},
		{"172.16.0.15", 22, ProtocolUDP, false},
		{"192.0.2.9", 53, ProtocolTCP, false},
		{"192.0.2.9", 53, ProtocolUDP, true},
		{"::ffff:10.10.3.4", 443, ProtocolTCP, true},
		{"10.10.3.4", 0, ProtocolTCP, false},
	} {
		got := resources.MatchIP(netip.MustParseAddr(test.address), test.port, test.protocol)
		if got != test.want {
			t.Fatalf("MatchIP(%s, %d, %s) = %t, want %t", test.address, test.port, test.protocol, got, test.want)
		}
	}
}

func TestResourcesMatchDomainHonorsSubdomainsAndPorts(t *testing.T) {
	t.Parallel()
	resources := testResources()
	for _, test := range []struct {
		name string
		port uint16
		want bool
	}{
		{"lib.example.edu", 80, true},
		{"LIB.example.edu.", 80, true},
		{"www.lib.example.edu", 80, false},
		{"intranet.example.edu", 443, true},
		{"git.intranet.example.edu", 443, true},
		{"git.intranet.example.edu", 80, false},
		{"notintranet.example.edu", 443, false},
		{"", 443, false},
	} {
		if got := resources.MatchDomain(test.name, test.port, ProtocolTCP); got != test.want {
			t.Fatalf("MatchDomain(%q, %d) = %t, want %t", test.name, test.port, got, test.want)
		}
	}
}

func TestResourcesLookupDNSOverrideIsExactAndCopies(t *testing.T) {
	t.Parallel()
	resources := testResources()
	addresses, ok := resources.LookupDNSOverride("GIT.intranet.example.edu.")
	if !ok || len(addresses) != 2 {
		t.Fatalf("override = %v, %t", addresses, ok)
	}
	addresses[0] = netip.Addr{}
	if again, _ := resources.LookupDNSOverride("git.intranet.example.edu"); !again[0].IsValid() {
		t.Fatal("LookupDNSOverride exposed its backing array")
	}
	if _, ok := resources.LookupDNSOverride("intranet.example.edu"); ok {
		t.Fatal("override matched a parent domain")
	}
}

func TestResourcesValidateRejectsMalformedRules(t *testing.T) {
	t.Parallel()
	valid := IPRule{Range: IPRangeFromPrefix(netip.MustParsePrefix("10.0.0.0/8")), Protocol: ProtocolTCP}
	for name, resources := range map[string]Resources{
		"empty protocol": {IPRules: []IPRule{{Range: valid.Range}}},
		"reversed range": {IPRules: []IPRule{{Range: IPRange{First: netip.MustParseAddr("10.0.0.9"), Last: netip.MustParseAddr("10.0.0.1")}, Protocol: ProtocolTCP}}},
		"mixed family":   {IPRules: []IPRule{{Range: IPRange{First: netip.MustParseAddr("10.0.0.1"), Last: netip.MustParseAddr("::1")}, Protocol: ProtocolTCP}}},
		"zero port":      {IPRules: []IPRule{{Range: valid.Range, Ports: []PortRange{{First: 0, Last: 10}}, Protocol: ProtocolTCP}}},
		"reversed ports": {IPRules: []IPRule{{Range: valid.Range, Ports: []PortRange{{First: 90, Last: 80}}, Protocol: ProtocolTCP}}},
		"wildcard name":  {DomainRules: []DomainRule{{Domain: "*.example.edu", Protocol: ProtocolTCP}}},
		"upper name":     {DomainRules: []DomainRule{{Domain: "Example.edu", Protocol: ProtocolTCP}}},
		"empty override": {DNSOverrides: []DNSOverride{{Domain: "a.example.edu"}}},
	} {
		if err := resources.Validate(); err == nil {
			t.Fatalf("%s: Validate() accepted %+v", name, resources)
		}
	}
	if err := (Resources{IPRules: []IPRule{valid}}).Validate(); err != nil {
		t.Fatalf("valid rule rejected: %v", err)
	}
}
