// Package runtime owns the native L3VPN session lifecycle. It contains no
// authentication, privilege acquisition, or host networking operations.
package runtime

import (
	"errors"
	"time"
)

type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
)

type Component uint8

const (
	ComponentCommand Component = iota
	ComponentRX
	ComponentTX
	ComponentSOCKS
	componentCount
)

type RenewalReason string

const (
	RenewalAddressChanged   RenewalReason = "address_changed"
	RenewalGatewayRejected  RenewalReason = "gateway_rejected"
	RenewalReconnectTimeout RenewalReason = "reconnect_timeout"
)

var ErrRenewalRequired = errors.New("session renewal required")
var ErrGatewayRejected = errors.New("gateway rejected authenticated transport")

// RenewalRequired contains only a fixed reason. Protocol replies and session
// material must never be wrapped in or attached to this error.
type RenewalRequired struct {
	Reason RenewalReason
}

func (err *RenewalRequired) Error() string { return ErrRenewalRequired.Error() }
func (err *RenewalRequired) Unwrap() error { return ErrRenewalRequired }

type FailureCode string

const (
	FailureTransportUnavailable FailureCode = "transport_unavailable"
	FailureProtocolInvalid      FailureCode = "protocol_invalid"
	FailureRuntimeStopped       FailureCode = "runtime_stopped"
)

// TransportFailure deliberately exposes an allowlisted code and no cause.
type TransportFailure struct {
	Code FailureCode
}

func (err *TransportFailure) Error() string {
	if err == nil {
		return string(FailureRuntimeStopped)
	}
	switch err.Code {
	case FailureTransportUnavailable, FailureProtocolInvalid, FailureRuntimeStopped:
		return string(err.Code)
	default:
		return string(FailureRuntimeStopped)
	}
}

type Readiness struct {
	ready             [componentCount]bool
	everConnected     bool
	reconnectingSince time.Time
	renewalIssued     bool
}

func (readiness *Readiness) Mark(component Component, ready bool, now time.Time) State {
	if component >= componentCount {
		return readiness.State()
	}
	readiness.ready[component] = ready
	if readiness.allReady() {
		readiness.everConnected = true
		readiness.reconnectingSince = time.Time{}
		return StateConnected
	}
	if readiness.everConnected {
		if readiness.reconnectingSince.IsZero() {
			readiness.reconnectingSince = now
		}
		return StateReconnecting
	}
	return StateConnecting
}

func (readiness *Readiness) State() State {
	if readiness.allReady() {
		return StateConnected
	}
	if readiness.everConnected {
		return StateReconnecting
	}
	return StateConnecting
}

func (readiness *Readiness) ReconnectingSince() (time.Time, bool) {
	return readiness.reconnectingSince, !readiness.reconnectingSince.IsZero()
}

func (readiness *Readiness) Watchdog(now time.Time, timeout time.Duration) error {
	if readiness.renewalIssued || readiness.State() != StateReconnecting || readiness.reconnectingSince.IsZero() {
		return nil
	}
	if now.Sub(readiness.reconnectingSince) < timeout {
		return nil
	}
	readiness.renewalIssued = true
	return &RenewalRequired{Reason: RenewalReconnectTimeout}
}

func (readiness *Readiness) allReady() bool {
	for _, ready := range readiness.ready {
		if !ready {
			return false
		}
	}
	return true
}
