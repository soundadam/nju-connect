package app

import (
	"sync"
	"time"

	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
	"github.com/soundadam/nju-connect/internal/traffic"
)

// RuntimeStatusTracker keeps the snapshot `status` reports for a running
// runtime. It is safe for concurrent use.
type RuntimeStatusTracker struct {
	mu       sync.RWMutex
	snapshot runtimecontrol.Snapshot
}

// NewRuntimeStatusTracker starts the snapshot a runtime publishes on its
// control socket.
func NewRuntimeStatusTracker(profile runtime.ProtocolProfileID) *RuntimeStatusTracker {
	startedAt := time.Now().UTC()
	return &RuntimeStatusTracker{snapshot: runtimecontrol.Snapshot{
		SchemaVersion:  runtimecontrol.SchemaVersion,
		Running:        true,
		State:          string(nativeapp.StateConnecting),
		StartedAt:      &startedAt,
		Profile:        profile,
		AccessEvidence: "unknown",
	}}
}

// Snapshot returns the current snapshot.
func (tracker *RuntimeStatusTracker) Snapshot() runtimecontrol.Snapshot {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.snapshot
}

// UpdateTraffic records the EasyConnect runtime's counters.
func (tracker *RuntimeStatusTracker) UpdateTraffic(snapshot nativeapp.TrafficSnapshot, sampledAt time.Time) {
	tracker.UpdateIngressTraffic(traffic.Snapshot{
		SessionStartedAt:  snapshot.SessionStartedAt,
		UploadBytes:       snapshot.UploadBytes,
		DownloadBytes:     snapshot.DownloadBytes,
		ActiveConnections: snapshot.ActiveConnections,
		TotalConnections:  snapshot.TotalConnections,
	}, sampledAt)
}

// UpdateIngressTraffic records counters measured at a backend's SOCKS
// ingress, such as the aTrust connection's.
func (tracker *RuntimeStatusTracker) UpdateIngressTraffic(snapshot traffic.Snapshot, sampledAt time.Time) {
	var sessionStartedAt *time.Time
	if !snapshot.SessionStartedAt.IsZero() {
		started := snapshot.SessionStartedAt.UTC()
		sessionStartedAt = &started
	}
	tracker.mu.Lock()
	tracker.snapshot.Traffic = &runtimecontrol.Traffic{
		SessionStartedAt: sessionStartedAt, SampledAtUnixMilli: sampledAt.UTC().UnixMilli(),
		UploadBytes: snapshot.UploadBytes, DownloadBytes: snapshot.DownloadBytes,
		ActiveConnections: snapshot.ActiveConnections, TotalConnections: snapshot.TotalConnections,
	}
	tracker.mu.Unlock()
}

// RuntimeStatusObserver records runtime events in tracker and forwards them
// to base.
func RuntimeStatusObserver(base nativeapp.ObserverFuncs, tracker *RuntimeStatusTracker) nativeapp.ObserverFuncs {
	return nativeapp.ObserverFuncs{
		OnState: func(state nativeapp.State) {
			tracker.mu.Lock()
			tracker.snapshot.State = string(state)
			tracker.mu.Unlock()
			base.StateChanged(state)
		},
		OnCommandFailure: func(failure nativeapp.CommandFailure) {
			tracker.mu.Lock()
			tracker.snapshot.LastCommandFailure = string(failure.Stage)
			tracker.mu.Unlock()
			base.CommandFailed(failure)
		},
		OnDataFailure: func(stage nativeapp.DataFailureStage) {
			tracker.mu.Lock()
			tracker.snapshot.LastDataFailure = string(stage)
			tracker.mu.Unlock()
			base.DataFailed(stage)
		},
		OnSOCKSListen: func(address string) {
			tracker.mu.Lock()
			tracker.snapshot.SOCKSListen = address
			tracker.mu.Unlock()
			base.SOCKSListening(address)
		},
		OnTraffic: func(snapshot nativeapp.TrafficSnapshot) {
			tracker.UpdateTraffic(snapshot, time.Now())
			base.TrafficChanged(snapshot)
		},
		OnAccessEvidence: func(available bool) {
			tracker.mu.Lock()
			if available {
				tracker.snapshot.AccessEvidence = "available"
			} else {
				tracker.snapshot.AccessEvidence = "unavailable"
			}
			tracker.mu.Unlock()
			base.AccessEvidence(available)
		},
	}
}
