// Package core owns soundconnect's connection state transitions and decisions.
package core

import (
	"errors"
	"fmt"

	"github.com/soundadam/soundconnect/internal/gatewayauth"
)

type DataplaneMode string

const (
	DataplaneNone   DataplaneMode = "none"
	DataplaneTCP    DataplaneMode = "tcp"
	DataplaneL3VPN  DataplaneMode = "l3vpn"
	DataplaneHybrid DataplaneMode = "hybrid"
)

type DataplanePlan struct {
	Mode               DataplaneMode
	ResourceCount      int
	InternalDNS        bool
	DedicatedLine      bool
	SecurityCheck      bool
	LocalAgentRequired bool
	BoundaryReady      bool
}

func BuildDataplanePlan(session gatewayauth.SessionState, bootstrap gatewayauth.Bootstrap) (DataplanePlan, error) {
	if !bootstrap.ConfigurationAvailable || !bootstrap.ResourcesAvailable {
		return DataplanePlan{}, errors.New("gateway bootstrap is incomplete")
	}
	plan := DataplanePlan{
		Mode:               dataplaneMode(bootstrap.Services),
		ResourceCount:      bootstrap.Resources.Web + bootstrap.Resources.TCP + bootstrap.Resources.L3VPN + bootstrap.Resources.Unknown,
		InternalDNS:        bootstrap.Services.InternalDNS,
		DedicatedLine:      bootstrap.Services.DedicatedLine,
		SecurityCheck:      bootstrap.Services.SecurityCheck,
		LocalAgentRequired: bootstrap.Services.LocalAgentRequired(),
	}
	if plan.LocalAgentRequired && !session.HasID {
		return DataplanePlan{}, fmt.Errorf("%s dataplane requires a gateway session identifier", plan.Mode)
	}
	plan.BoundaryReady = plan.LocalAgentRequired && session.HasID
	return plan, nil
}

func dataplaneMode(requirements gatewayauth.ServiceRequirements) DataplaneMode {
	switch {
	case requirements.TCP && requirements.L3VPN:
		return DataplaneHybrid
	case requirements.TCP:
		return DataplaneTCP
	case requirements.L3VPN:
		return DataplaneL3VPN
	default:
		return DataplaneNone
	}
}
