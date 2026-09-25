package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/dial"
)

const (
	atrustOAuthAuthType    = "auth/httpsOauth2"
	atrustPasswordAuthType = "auth/psw"
)

// parseATrustEndpoint turns a configured host or host:port into the aTrust
// transport endpoint, including the pinned NJU gateway address.
func parseATrustEndpoint(server string) (backend.Endpoint, error) {
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

// selectATrustAuthenticationMethod chooses one of the methods advertised by
// the gateway. Keeping this selection tied to discovery avoids hard-coding
// NJU's tenant-generated login domains while still allowing an explicit
// password-authentication opt-in.
func selectATrustAuthenticationMethod(
	methods []backend.AuthenticationMethod,
	requestedType string,
	requestedDomain string,
) (backend.AuthenticationMethod, error) {
	requestedType = strings.TrimSpace(requestedType)
	requestedDomain = strings.TrimSpace(requestedDomain)
	if requestedType == "" {
		requestedType = atrustOAuthAuthType
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

func validateATrustAuthenticationType(authType string) error {
	switch strings.TrimSpace(authType) {
	case atrustOAuthAuthType, atrustPasswordAuthType:
		return nil
	default:
		return errors.New("aTrust authentication type must be auth/httpsOauth2 or auth/psw")
	}
}
