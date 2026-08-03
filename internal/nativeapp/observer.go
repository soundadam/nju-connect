package nativeapp

import (
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/traffic"
)

type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
)

// TrafficSnapshot is deliberately limited to application payload and SOCKS
// connection counters. It cannot carry gateway identities or wire data.
type TrafficSnapshot struct {
	SessionStartedAt  time.Time
	UploadBytes       uint64
	DownloadBytes     uint64
	ActiveConnections int64
	TotalConnections  uint64
}

// Observer is the minimal sanitized bridge used by CLI and future UI layers.
// Implementations must tolerate callbacks from runtime-owned goroutines.
type Observer interface {
	StateChanged(State)
	SOCKSListening(string)
	TrafficChanged(TrafficSnapshot)
	AccessEvidence(bool)
}

// ObserverFuncs adapts optional callbacks to Observer.
type ObserverFuncs struct {
	OnState          func(State)
	OnSOCKSListen    func(string)
	OnTraffic        func(TrafficSnapshot)
	OnAccessEvidence func(bool)
}

func (observer ObserverFuncs) StateChanged(state State) {
	if observer.OnState != nil {
		observer.OnState(state)
	}
}

func (observer ObserverFuncs) SOCKSListening(address string) {
	if observer.OnSOCKSListen != nil {
		observer.OnSOCKSListen(address)
	}
}

func (observer ObserverFuncs) TrafficChanged(snapshot TrafficSnapshot) {
	if observer.OnTraffic != nil {
		observer.OnTraffic(snapshot)
	}
}

func (observer ObserverFuncs) AccessEvidence(available bool) {
	if observer.OnAccessEvidence != nil {
		observer.OnAccessEvidence(available)
	}
}

type serializedObserver struct {
	mu       sync.Mutex
	observer Observer
}

func newSerializedObserver(observer Observer) *serializedObserver {
	return &serializedObserver{observer: observer}
}

func (observer *serializedObserver) state(runtimeState runtime.State) {
	state, valid := sanitizedState(runtimeState)
	if !valid || observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.StateChanged(state)
}

func (observer *serializedObserver) listen(address net.Addr) {
	listen, valid := sanitizedLoopbackAddress(address)
	if !valid || observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.SOCKSListening(listen)
}

func (observer *serializedObserver) trafficSnapshot(snapshot traffic.Snapshot) {
	if observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.TrafficChanged(sanitizedRuntimeTraffic(snapshot))
}

func (observer *serializedObserver) accessEvidence(available bool) {
	if observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.AccessEvidence(available)
}

func sanitizedState(state runtime.State) (State, bool) {
	switch state {
	case runtime.StateConnecting:
		return StateConnecting, true
	case runtime.StateConnected:
		return StateConnected, true
	case runtime.StateReconnecting:
		return StateReconnecting, true
	default:
		return "", false
	}
}

func sanitizedLoopbackAddress(address net.Addr) (string, bool) {
	if address == nil {
		return "", false
	}
	host, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return "", false
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil || !parsed.IsLoopback() {
		return "", false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", false
	}
	return net.JoinHostPort(parsed.String(), strconv.FormatUint(port, 10)), true
}

func sanitizedRuntimeTraffic(snapshot traffic.Snapshot) TrafficSnapshot {
	return TrafficSnapshot{
		SessionStartedAt:  snapshot.SessionStartedAt,
		UploadBytes:       snapshot.UploadBytes,
		DownloadBytes:     snapshot.DownloadBytes,
		ActiveConnections: snapshot.ActiveConnections,
		TotalConnections:  snapshot.TotalConnections,
	}
}
