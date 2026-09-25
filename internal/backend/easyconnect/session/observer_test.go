package nativeapp

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/traffic"
)

func TestObserverAllowsOnlyNamedStateAndLoopbackListener(t *testing.T) {
	fixedNow := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.FixedZone("secret-location", 8*60*60))
	var states []State
	var commandFailures []CommandFailure
	var dataFailures []DataFailureStage
	var listeners []string
	observer := newSerializedObserver(ObserverFuncs{
		OnState: func(state State) {
			states = append(states, state)
		},
		OnSOCKSListen: func(address string) {
			listeners = append(listeners, address)
		},
		OnCommandFailure: func(failure CommandFailure) {
			commandFailures = append(commandFailures, failure)
		},
		OnDataFailure: func(stage DataFailureStage) {
			dataFailures = append(dataFailures, stage)
		},
	})

	observer.state(runtime.StateConnecting)
	observer.state(runtime.State("gateway-reply-secret"))
	observer.commandFailure(runtime.CommandFailure{Attempt: 2, Stage: runtime.StageSendIPReadFailed, At: fixedNow})
	observer.commandFailure(runtime.CommandFailure{Attempt: 3, Stage: runtime.FailureStage("gateway-reply-secret"), At: fixedNow})
	observer.dataFailure(runtime.StageRXHandshakeFailed)
	observer.dataFailure(runtime.StageRXInvalidIPv4)
	observer.dataFailure(runtime.FailureStage("gateway-reply-secret"))
	observer.listen(testAddress("127.0.0.1:1081"))
	observer.listen(testAddress("10.0.0.1:1081"))

	if len(states) != 1 || states[0] != StateConnecting {
		t.Fatalf("states = %v", states)
	}
	wantFailure := CommandFailure{Attempt: 2, Stage: CommandSendIPReadFailed, At: fixedNow.UTC()}
	if len(commandFailures) != 1 || commandFailures[0] != wantFailure {
		t.Fatalf("command failures = %v", commandFailures)
	}
	if len(dataFailures) != 2 || dataFailures[0] != DataRXHandshakeFailed || dataFailures[1] != DataRXInvalidIPv4 {
		t.Fatalf("data failures = %v", dataFailures)
	}
	if len(listeners) != 1 || listeners[0] != "127.0.0.1:1081" {
		t.Fatalf("listeners = %v", listeners)
	}
}

func TestObserverFuncsRejectsUntrustedCommandStage(t *testing.T) {
	fixedNow := time.Unix(1000, 0)
	var failures []CommandFailure
	observer := ObserverFuncs{OnCommandFailure: func(failure CommandFailure) {
		failures = append(failures, failure)
	}}
	observer.CommandFailed(CommandFailure{Attempt: 1, Stage: CommandSendIPReadFailed, At: fixedNow})
	observer.CommandFailed(CommandFailure{Attempt: 2, Stage: CommandFailureStage("gateway-reply-secret"), At: fixedNow})
	observer.CommandFailed(CommandFailure{Stage: CommandSendIPReadFailed, At: fixedNow})
	observer.CommandFailed(CommandFailure{Attempt: 3, Stage: CommandSendIPReadFailed})
	if len(failures) != 1 || failures[0] != (CommandFailure{Attempt: 1, Stage: CommandSendIPReadFailed, At: fixedNow}) {
		t.Fatalf("command failures = %v", failures)
	}
}

func TestObserverSerializesRuntimeCallbacks(t *testing.T) {
	const callsPerKind = 40
	var callbackMu sync.Mutex
	active := 0
	concurrent := false
	callback := func() {
		callbackMu.Lock()
		active++
		if active != 1 {
			concurrent = true
		}
		callbackMu.Unlock()
		callbackMu.Lock()
		active--
		callbackMu.Unlock()
	}
	observer := newSerializedObserver(ObserverFuncs{
		OnState:          func(State) { callback() },
		OnCommandFailure: func(CommandFailure) { callback() },
		OnDataFailure:    func(DataFailureStage) { callback() },
		OnSOCKSListen:    func(string) { callback() },
		OnTraffic:        func(TrafficSnapshot) { callback() },
		OnAccessEvidence: func(bool) { callback() },
	})

	var workers sync.WaitGroup
	for range callsPerKind {
		workers.Add(6)
		go func() {
			defer workers.Done()
			observer.state(runtime.StateConnected)
		}()
		go func() {
			defer workers.Done()
			observer.commandFailure(runtime.CommandFailure{Attempt: 1, Stage: runtime.StageUpstreamConnectFailed, At: time.Unix(1000, 0)})
		}()
		go func() {
			defer workers.Done()
			observer.dataFailure(runtime.StageTXHandshakeFailed)
		}()
		go func() {
			defer workers.Done()
			observer.listen(testAddress("[::1]:1081"))
		}()
		go func() {
			defer workers.Done()
			observer.trafficSnapshot(traffic.Snapshot{})
		}()
		go func() {
			defer workers.Done()
			observer.accessEvidence(true)
		}()
	}
	workers.Wait()
	if concurrent {
		t.Fatal("observer callbacks ran concurrently")
	}
}

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

var _ net.Addr = testAddress("")
