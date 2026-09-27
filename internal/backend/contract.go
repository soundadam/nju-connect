// Package backend defines the stable boundary between the nju-connect
// application and protocol-specific VPN implementations.
package backend

import (
	"errors"
	"strings"
)

type Name string

const (
	EasyConnect Name = "easyconnect"
	ATrust      Name = "atrust"

	DefaultEasyConnectGateway = "vpn.nju.edu.cn"
	DefaultATrustGateway      = "vpn.nju.edu.cn"

	// NJU serves aTrust under the vpn.nju.edu.cn virtual host on a separate
	// gateway while public DNS still resolves that name to the EasyConnect
	// appliance, so the aTrust transport dials this address explicitly.
	DefaultATrustGatewayAddress = "219.219.118.20"

	// legacyATrustGateway no longer serves aTrust; saved profiles naming it
	// are redirected to the current gateway.
	legacyATrustGateway = "ztna.nju.edu.cn"
)

// ErrCredentialRejected reports that the gateway refused the saved username
// or password. Callers can offer to change them and try again; retrying with
// the same values cannot succeed.
var ErrCredentialRejected = errors.New("gateway rejected the username or password")

type AuthenticationCapability string

const (
	AuthenticationSharedPassword AuthenticationCapability = "shared_password"
	AuthenticationVerification   AuthenticationCapability = "verification_code"
	AuthenticationOAuth          AuthenticationCapability = "oauth"
)

// Descriptor is safe for presentation layers. It contains product metadata
// and capabilities only, never credentials or protocol-specific login state.
type Descriptor struct {
	ID             Name                       `json:"id"`
	DisplayName    string                     `json:"display_name"`
	ShortName      string                     `json:"short_name"`
	DefaultGateway string                     `json:"default_gateway"`
	Authentication []AuthenticationCapability `json:"authentication"`
}

func Catalog() []Descriptor {
	return []Descriptor{
		{
			ID:             EasyConnect,
			DisplayName:    "EasyConnect",
			ShortName:      "easyconnect",
			DefaultGateway: DefaultEasyConnectGateway,
			Authentication: []AuthenticationCapability{
				AuthenticationSharedPassword,
				AuthenticationVerification,
			},
		},
		{
			ID:             ATrust,
			DisplayName:    "aTrust",
			ShortName:      "aTrust",
			DefaultGateway: DefaultATrustGateway,
			Authentication: []AuthenticationCapability{
				AuthenticationSharedPassword,
				AuthenticationVerification,
				AuthenticationOAuth,
			},
		},
	}
}

func ParseName(value string) (Name, error) {
	name := Name(strings.ToLower(strings.TrimSpace(value)))
	switch name {
	case EasyConnect, ATrust:
		return name, nil
	default:
		return "", errors.New("unsupported protocol backend")
	}
}

// Endpoint is deliberately protocol-neutral. Backends own their own TLS,
// authentication, resource-discovery, and tunnel semantics.
type Endpoint struct {
	Host string
	Port int
	// Address, when set, is dialed instead of Host. Host remains the gateway
	// identity used for OAuth callback matching and presentation.
	Address string
}

// DialHost returns the host the transport connects to.
func (endpoint Endpoint) DialHost() string {
	if endpoint.Address != "" {
		return endpoint.Address
	}
	return endpoint.Host
}

// ATrustEndpoint maps a configured aTrust gateway to its transport endpoint,
// pinning the NJU gateway address that public DNS does not yet publish.
func ATrustEndpoint(host string, port int) Endpoint {
	endpoint := Endpoint{Host: host, Port: port}
	if strings.EqualFold(host, legacyATrustGateway) {
		endpoint.Host = DefaultATrustGateway
	}
	if strings.EqualFold(endpoint.Host, DefaultATrustGateway) {
		endpoint.Address = DefaultATrustGatewayAddress
	}
	return endpoint
}

type AuthenticationMethod struct {
	Domain   string `json:"domain"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	LoginURL string `json:"login_url,omitempty"`
}
