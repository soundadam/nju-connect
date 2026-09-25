package atrustbackend

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Protocol restricts a resource rule to a transport protocol.
type Protocol string

const (
	ProtocolAll Protocol = "all"
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

func (protocol Protocol) valid() bool {
	switch protocol {
	case ProtocolAll, ProtocolTCP, ProtocolUDP:
		return true
	default:
		return false
	}
}

func (protocol Protocol) permits(requested Protocol) bool {
	return protocol == ProtocolAll || protocol == requested
}

// PortRange is an inclusive destination port range.
type PortRange struct {
	First uint16
	Last  uint16
}

// AllPorts is the port set that matches every non-zero destination port.
func AllPorts() []PortRange { return []PortRange{{First: 1, Last: 65535}} }

func (portRange PortRange) Contains(port uint16) bool {
	return port >= portRange.First && port <= portRange.Last
}

func (portRange PortRange) validate() error {
	if portRange.First == 0 || portRange.Last < portRange.First {
		return fmt.Errorf("invalid port range %d-%d", portRange.First, portRange.Last)
	}
	return nil
}

// IPRange is an inclusive address range within one address family.
type IPRange struct {
	First netip.Addr
	Last  netip.Addr
}

// IPRangeFromPrefix returns the range covered by prefix.
func IPRangeFromPrefix(prefix netip.Prefix) IPRange {
	prefix = prefix.Masked()
	first := prefix.Addr()
	last := first.AsSlice()
	bits := prefix.Bits()
	for index := range last {
		for bit := 0; bit < 8; bit++ {
			if index*8+bit >= bits {
				last[index] |= 0x80 >> bit
			}
		}
	}
	lastAddr, _ := netip.AddrFromSlice(last)
	return IPRange{First: first, Last: lastAddr}
}

func (ipRange IPRange) Contains(address netip.Addr) bool {
	address = address.Unmap()
	return address.BitLen() == ipRange.First.BitLen() &&
		ipRange.First.Compare(address) <= 0 && address.Compare(ipRange.Last) <= 0
}

func (ipRange IPRange) validate() error {
	if !ipRange.First.IsValid() || !ipRange.Last.IsValid() {
		return errors.New("IP range bounds are required")
	}
	if ipRange.First.Is4In6() || ipRange.Last.Is4In6() || ipRange.First.Zone() != "" || ipRange.Last.Zone() != "" {
		return errors.New("IP range bounds must be unmapped and zone-free")
	}
	if ipRange.First.BitLen() != ipRange.Last.BitLen() || ipRange.Last.Less(ipRange.First) {
		return fmt.Errorf("invalid IP range %s-%s", ipRange.First, ipRange.Last)
	}
	return nil
}

// IPRule authorizes an address range. Empty Ports matches every port.
type IPRule struct {
	Range    IPRange
	Ports    []PortRange
	Protocol Protocol
}

// DomainRule authorizes a DNS name, and optionally every name beneath it.
// Domain is a plain lower-case name without wildcards or a trailing dot.
// Empty Ports matches every port.
type DomainRule struct {
	Domain            string
	IncludeSubdomains bool
	Ports             []PortRange
	Protocol          Protocol
}

// DNSOverride pins a DNS name to gateway-provided addresses.
type DNSOverride struct {
	Domain    string
	Addresses []netip.Addr
}

// Resources is the set of destinations an authenticated account may reach
// through the tunnel. It is SoundConnect's model; a protocol core translates
// its wire format into these types.
type Resources struct {
	IPRules      []IPRule
	DomainRules  []DomainRule
	DNSOverrides []DNSOverride
}

// Validate rejects malformed rules so routing never guesses.
func (resources Resources) Validate() error {
	for index, rule := range resources.IPRules {
		if err := rule.Range.validate(); err != nil {
			return fmt.Errorf("IP rule %d: %w", index, err)
		}
		if err := validateRuleShape(rule.Ports, rule.Protocol); err != nil {
			return fmt.Errorf("IP rule %d: %w", index, err)
		}
	}
	for index, rule := range resources.DomainRules {
		if err := validateDomain(rule.Domain); err != nil {
			return fmt.Errorf("domain rule %d: %w", index, err)
		}
		if err := validateRuleShape(rule.Ports, rule.Protocol); err != nil {
			return fmt.Errorf("domain rule %d: %w", index, err)
		}
	}
	for index, override := range resources.DNSOverrides {
		if err := validateDomain(override.Domain); err != nil {
			return fmt.Errorf("DNS override %d: %w", index, err)
		}
		if len(override.Addresses) == 0 {
			return fmt.Errorf("DNS override %d: at least one address is required", index)
		}
		for _, address := range override.Addresses {
			if !address.IsValid() || address.Is4In6() || address.Zone() != "" {
				return fmt.Errorf("DNS override %d: invalid address", index)
			}
		}
	}
	return nil
}

// MatchIP reports whether destination is an authorized resource address.
func (resources Resources) MatchIP(address netip.Addr, port uint16, protocol Protocol) bool {
	for _, rule := range resources.IPRules {
		if rule.Protocol.permits(protocol) && rule.Range.Contains(address) && portsContain(rule.Ports, port) {
			return true
		}
	}
	return false
}

// MatchDomain reports whether name is an authorized resource name.
func (resources Resources) MatchDomain(name string, port uint16, protocol Protocol) bool {
	name = normalizeDomain(name)
	if name == "" {
		return false
	}
	for _, rule := range resources.DomainRules {
		if !rule.Protocol.permits(protocol) || !portsContain(rule.Ports, port) {
			continue
		}
		if name == rule.Domain || (rule.IncludeSubdomains && strings.HasSuffix(name, "."+rule.Domain)) {
			return true
		}
	}
	return false
}

// LookupDNSOverride returns the pinned addresses for name, if any.
func (resources Resources) LookupDNSOverride(name string) ([]netip.Addr, bool) {
	name = normalizeDomain(name)
	for _, override := range resources.DNSOverrides {
		if override.Domain == name {
			return append([]netip.Addr(nil), override.Addresses...), true
		}
	}
	return nil, false
}

func validateRuleShape(ports []PortRange, protocol Protocol) error {
	if !protocol.valid() {
		return fmt.Errorf("unsupported protocol %q", protocol)
	}
	for _, portRange := range ports {
		if err := portRange.validate(); err != nil {
			return err
		}
	}
	return nil
}

func portsContain(ports []PortRange, port uint16) bool {
	if port == 0 {
		return false
	}
	if len(ports) == 0 {
		return true
	}
	for _, portRange := range ports {
		if portRange.Contains(port) {
			return true
		}
	}
	return false
}

func normalizeDomain(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

func validateDomain(domain string) error {
	if domain == "" || len(domain) > 253 {
		return errors.New("domain is empty or too long")
	}
	if domain != normalizeDomain(domain) {
		return fmt.Errorf("domain %q must be lower-case without a trailing dot", domain)
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("domain %q has an invalid label", domain)
		}
		for _, character := range label {
			if !(character == '-' || character == '_' ||
				(character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
				return fmt.Errorf("domain %q contains an invalid character", domain)
			}
		}
	}
	return nil
}
