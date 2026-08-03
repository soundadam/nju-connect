package nativeapp

import (
	"net"
	"sync"
	"testing"

	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/traffic"
)

func TestObserverAllowsOnlyNamedStateAndLoopbackListener(t *testing.T) {
	var states []State
	var listeners []string
	observer := newSerializedObserver(ObserverFuncs{
		OnState: func(state State) {
			states = append(states, state)
		},
		OnSOCKSListen: func(address string) {
			listeners = append(listeners, address)
		},
	})

	observer.state(runtime.StateConnecting)
	observer.state(runtime.State("gateway-reply-secret"))
	observer.listen(testAddress("127.0.0.1:1081"))
	observer.listen(testAddress("10.0.0.1:1081"))

	if len(states) != 1 || states[0] != StateConnecting {
		t.Fatalf("states = %v", states)
	}
	if len(listeners) != 1 || listeners[0] != "127.0.0.1:1081" {
		t.Fatalf("listeners = %v", listeners)
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
		OnSOCKSListen:    func(string) { callback() },
		OnTraffic:        func(TrafficSnapshot) { callback() },
		OnAccessEvidence: func(bool) { callback() },
	})

	var workers sync.WaitGroup
	for range callsPerKind {
		workers.Add(4)
		go func() {
			defer workers.Done()
			observer.state(runtime.StateConnected)
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
