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

type CommandFailureStage string

const (
	CommandUpstreamConnectFailed        CommandFailureStage = CommandFailureStage(runtime.StageUpstreamConnectFailed)
	CommandProtocolTLSHandshakeFailed   CommandFailureStage = CommandFailureStage(runtime.StageProtocolTLSHandshakeFailed)
	CommandProtocolTLSCertificateFailed CommandFailureStage = CommandFailureStage(runtime.StageProtocolTLSCertificateFailed)
	CommandProtocolPrefaceFailed        CommandFailureStage = CommandFailureStage(runtime.StageProtocolPrefaceFailed)
	CommandSendIPWriteFailed            CommandFailureStage = CommandFailureStage(runtime.StageSendIPWriteFailed)
	CommandSendIPReadFailed             CommandFailureStage = CommandFailureStage(runtime.StageSendIPReadFailed)
	CommandSendIPRejected               CommandFailureStage = CommandFailureStage(runtime.StageSendIPRejected)
)

type CommandFailure struct {
	Attempt uint64
	Stage   CommandFailureStage
	At      time.Time
}

type DataFailureStage string

const (
	DataRXHandshakeFailed DataFailureStage = DataFailureStage(runtime.StageRXHandshakeFailed)
	DataTXHandshakeFailed DataFailureStage = DataFailureStage(runtime.StageTXHandshakeFailed)
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
	CommandFailed(CommandFailure)
	DataFailed(DataFailureStage)
	SOCKSListening(string)
	TrafficChanged(TrafficSnapshot)
	AccessEvidence(bool)
}

// ObserverFuncs adapts optional callbacks to Observer.
type ObserverFuncs struct {
	OnState          func(State)
	OnCommandFailure func(CommandFailure)
	OnDataFailure    func(DataFailureStage)
	OnSOCKSListen    func(string)
	OnTraffic        func(TrafficSnapshot)
	OnAccessEvidence func(bool)
}

func (observer ObserverFuncs) StateChanged(state State) {
	if observer.OnState != nil {
		observer.OnState(state)
	}
}

func (observer ObserverFuncs) CommandFailed(failure CommandFailure) {
	if failure.Attempt != 0 && !failure.At.IsZero() && validCommandFailureStage(failure.Stage) && observer.OnCommandFailure != nil {
		observer.OnCommandFailure(failure)
	}
}

func (observer ObserverFuncs) DataFailed(stage DataFailureStage) {
	if validDataFailureStage(stage) && observer.OnDataFailure != nil {
		observer.OnDataFailure(stage)
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

func (observer *serializedObserver) commandFailure(runtimeFailure runtime.CommandFailure) {
	stage, valid := sanitizedCommandFailureStage(runtimeFailure.Stage)
	if !valid || runtimeFailure.Attempt == 0 || runtimeFailure.At.IsZero() || observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.CommandFailed(CommandFailure{
		Attempt: runtimeFailure.Attempt,
		Stage:   stage,
		At:      runtimeFailure.At.UTC(),
	})
}

func (observer *serializedObserver) dataFailure(runtimeStage runtime.FailureStage) {
	stage, valid := sanitizedDataFailureStage(runtimeStage)
	if !valid || observer.observer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.observer.DataFailed(stage)
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

func sanitizedCommandFailureStage(stage runtime.FailureStage) (CommandFailureStage, bool) {
	switch stage {
	case runtime.StageUpstreamConnectFailed:
		return CommandUpstreamConnectFailed, true
	case runtime.StageProtocolTLSHandshakeFailed:
		return CommandProtocolTLSHandshakeFailed, true
	case runtime.StageProtocolTLSCertificateFailed:
		return CommandProtocolTLSCertificateFailed, true
	case runtime.StageProtocolPrefaceFailed:
		return CommandProtocolPrefaceFailed, true
	case runtime.StageSendIPWriteFailed:
		return CommandSendIPWriteFailed, true
	case runtime.StageSendIPReadFailed:
		return CommandSendIPReadFailed, true
	case runtime.StageSendIPRejected:
		return CommandSendIPRejected, true
	default:
		return "", false
	}
}

func validCommandFailureStage(stage CommandFailureStage) bool {
	switch stage {
	case CommandUpstreamConnectFailed,
		CommandProtocolTLSHandshakeFailed,
		CommandProtocolTLSCertificateFailed,
		CommandProtocolPrefaceFailed,
		CommandSendIPWriteFailed,
		CommandSendIPReadFailed,
		CommandSendIPRejected:
		return true
	default:
		return false
	}
}

func sanitizedDataFailureStage(stage runtime.FailureStage) (DataFailureStage, bool) {
	switch stage {
	case runtime.StageRXHandshakeFailed:
		return DataRXHandshakeFailed, true
	case runtime.StageTXHandshakeFailed:
		return DataTXHandshakeFailed, true
	default:
		return "", false
	}
}

func validDataFailureStage(stage DataFailureStage) bool {
	return stage == DataRXHandshakeFailed || stage == DataTXHandshakeFailed
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
