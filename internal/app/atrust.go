package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/dial"
)

// aTrust authentication types the CLI supports.
const (
	ATrustOAuthAuthType    = "auth/httpsOauth2"
	ATrustPasswordAuthType = "auth/psw"
)

// ATrustDiscoveryTimeout bounds one public discovery of a gateway's methods.
const ATrustDiscoveryTimeout = 25 * time.Second

// ParseATrustEndpoint turns a configured host or host:port into the aTrust
// transport endpoint, including the pinned NJU gateway address.
func ParseATrustEndpoint(server string) (backend.Endpoint, error) {
	host, address, err := dial.SplitServer(server)
	if err != nil {
		return backend.Endpoint{}, err
	}
	_, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return backend.Endpoint{}, errors.New("invalid port")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		return backend.Endpoint{}, errors.New("invalid port")
	}
	return backend.ATrustEndpoint(host, port), nil
}

// SelectATrustAuthenticationMethod chooses one of the methods advertised by
// the gateway. Keeping this selection tied to discovery avoids hard-coding
// NJU's tenant-generated login domains while still allowing an explicit
// password-authentication opt-in.
func SelectATrustAuthenticationMethod(
	methods []backend.AuthenticationMethod,
	requestedType string,
	requestedDomain string,
) (backend.AuthenticationMethod, error) {
	requestedType = strings.TrimSpace(requestedType)
	requestedDomain = strings.TrimSpace(requestedDomain)
	if requestedType == "" {
		requestedType = ATrustOAuthAuthType
	}
	for _, method := range methods {
		if method.Type != requestedType {
			continue
		}
		if requestedDomain != "" && method.Domain != requestedDomain {
			continue
		}
		return method, nil
	}
	if requestedDomain == "" {
		return backend.AuthenticationMethod{}, fmt.Errorf("aTrust authentication method %q is unavailable", requestedType)
	}
	return backend.AuthenticationMethod{}, fmt.Errorf("aTrust authentication method %q for domain %q is unavailable", requestedType, requestedDomain)
}

// ValidateATrustAuthenticationType accepts the types the CLI can complete.
func ValidateATrustAuthenticationType(authType string) error {
	switch strings.TrimSpace(authType) {
	case ATrustOAuthAuthType, ATrustPasswordAuthType:
		return nil
	default:
		return errors.New("aTrust authentication type must be auth/httpsOauth2 or auth/psw")
	}
}

// DiscoverATrust lists the authentication methods a gateway advertises,
// without logging in. It backs `auth-info`.
func DiscoverATrust(ctx context.Context, deps Deps, backendValue, server string) ([]backend.AuthenticationMethod, error) {
	backendName, err := backend.ParseName(backendValue)
	if err != nil {
		return nil, Usagef("select protocol backend: %w", err)
	}
	if backendName != backend.ATrust {
		return nil, Usagef("auth-info is currently available only for the aTrust backend")
	}
	endpoint, err := ParseATrustEndpoint(server)
	if err != nil {
		return nil, Usagef("parse aTrust gateway: %w", err)
	}
	return discoverATrust(ctx, deps, endpoint)
}

func discoverATrust(ctx context.Context, deps Deps, endpoint backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	ctx, cancel := context.WithTimeout(ctx, ATrustDiscoveryTimeout)
	defer cancel()
	methods, err := (atrustbackend.Discovery{Core: deps.ATrustCore()}).Discover(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("discover aTrust authentication: %w", err)
	}
	return methods, nil
}
