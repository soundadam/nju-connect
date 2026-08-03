// Package nativeapp connects an authenticated gateway boundary to the native
// userspace runtime. It deliberately owns no credentials or authentication
// retry policy.
package nativeapp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/dial"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
	"github.com/soundadam/soundconnect/internal/traffic"
)

const (
	defaultGatewayDialTimeout = 15 * time.Second
)

// SessionConfig contains only the already-authenticated handoff and
// non-secret connection policy. NativeGatewayToken is borrowed for the call to
// NewSession only; the caller remains responsible for clearing its temporary
// copy immediately after NewSession returns.
type SessionConfig struct {
	Settings           config.Config
	Plan               core.DataplanePlan
	NativeGatewayToken sessiontoken.NativeGatewayToken
	ResolveGatewayIP   string
	AccessProbeURL     string
	ResolveIPv4        runtime.ResolveIPv4Func
	MaxSOCKSClients    int
	Observer           Observer
	DialTimeout        time.Duration
}

// Session exposes only lifecycle and sanitized application telemetry. It does
// not expose the runtime's gateway identity or retained token material.
type Session struct {
	native   nativeSession
	observer *serializedObserver
}

type nativeSession interface {
	Run(context.Context) error
	Close() error
	Traffic() traffic.Snapshot
}

// NewSession constructs a production native runtime without dialing the
// gateway. The caller should perform the upstream-proxy preflight before
// obtaining credentials and invoking this function.
func NewSession(sessionConfig SessionConfig) (*Session, error) {
	if err := sessionConfig.Settings.Validate(); err != nil {
		return nil, err
	}
	if len(sessionConfig.NativeGatewayToken) != sessiontoken.NativeGatewayTokenSize {
		return nil, errors.New("native gateway token has an invalid length")
	}
	target, err := gatewayTargetFor(sessionConfig.Settings.Server, sessionConfig.ResolveGatewayIP)
	if err != nil {
		return nil, err
	}
	timeout := sessionConfig.DialTimeout
	if timeout <= 0 {
		timeout = defaultGatewayDialTimeout
	}
	outbound, err := dial.New(sessionConfig.Settings.UpstreamProxy, timeout)
	if err != nil {
		return nil, err
	}
	protocolDialer, err := runtime.NewProtocolTLSDialer(runtime.ProtocolTLSDialerConfig{
		Dial:        commandDialer(outbound, target.address),
		ServerName:  target.serverName,
		TLSInsecure: sessionConfig.Settings.TLSInsecure,
	})
	if err != nil {
		return nil, err
	}

	observer := newSerializedObserver(sessionConfig.Observer)
	native, err := runtime.NewNativeSession(runtime.NativeSessionConfig{
		Plan:             sessionConfig.Plan,
		AgentToken:       sessionConfig.NativeGatewayToken,
		CommandDial:      protocolDialer.Dial,
		SOCKSBind:        sessionConfig.Settings.SOCKSListen,
		ResolveIPv4:      sessionConfig.ResolveIPv4,
		MaxSOCKSClients:  sessionConfig.MaxSOCKSClients,
		AccessURL:        sessionConfig.AccessProbeURL,
		OnAccessEvidence: observer.accessEvidence,
		OnState:          observer.state,
		OnListen:         observer.listen,
	})
	if err != nil {
		return nil, err
	}
	return &Session{native: native, observer: observer}, nil
}

func (session *Session) Run(ctx context.Context) error {
	if session == nil || session.native == nil {
		return errors.New("native application session is unavailable")
	}
	err := session.native.Run(ctx)
	session.observer.trafficSnapshot(session.native.Traffic())
	return err
}

func (session *Session) Close() error {
	if session == nil || session.native == nil {
		return nil
	}
	return session.native.Close()
}

func (session *Session) Traffic() TrafficSnapshot {
	if session == nil || session.native == nil {
		return TrafficSnapshot{}
	}
	return sanitizedRuntimeTraffic(session.native.Traffic())
}

type gatewayTarget struct {
	serverName string
	address    string
}

func gatewayTargetFor(server string, resolveIP string) (gatewayTarget, error) {
	host, address, err := dial.SplitServer(server)
	if err != nil {
		return gatewayTarget{}, err
	}
	if strings.TrimSpace(resolveIP) == "" {
		return gatewayTarget{serverName: host, address: address}, nil
	}
	resolved, err := netip.ParseAddr(strings.TrimSpace(resolveIP))
	if err != nil {
		return gatewayTarget{}, errors.New("resolved gateway address must be a numeric IP")
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return gatewayTarget{}, errors.New("gateway address has an invalid port")
	}
	return gatewayTarget{
		serverName: host,
		address:    net.JoinHostPort(resolved.String(), port),
	}, nil
}

func commandDialer(outbound dial.ContextFunc, address string) runtime.CommandDialer {
	return func(ctx context.Context) (net.Conn, error) {
		return outbound(ctx, "tcp", address)
	}
}
