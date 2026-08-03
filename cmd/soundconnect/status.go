package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/soundadam/soundconnect/internal/nativeapp"
	"github.com/soundadam/soundconnect/internal/runtime"
)

const runtimeStatusSchema = 1

var errRuntimeNotRunning = errors.New("native runtime is not running")

type runtimeTrafficStatus struct {
	SessionStartedAt  *time.Time `json:"session_started_at,omitempty"`
	UploadBytes       uint64     `json:"upload_bytes"`
	DownloadBytes     uint64     `json:"download_bytes"`
	ActiveConnections int64      `json:"active_connections"`
	TotalConnections  uint64     `json:"total_connections"`
}

type runtimeStatusSnapshot struct {
	SchemaVersion      int                       `json:"schema_version"`
	Running            bool                      `json:"running"`
	State              string                    `json:"state"`
	StartedAt          *time.Time                `json:"started_at,omitempty"`
	Profile            runtime.ProtocolProfileID `json:"profile,omitempty"`
	SOCKSListen        string                    `json:"socks_listen,omitempty"`
	AccessEvidence     string                    `json:"access_evidence"`
	LastCommandFailure string                    `json:"last_command_failure,omitempty"`
	LastDataFailure    string                    `json:"last_data_failure,omitempty"`
	Traffic            *runtimeTrafficStatus     `json:"traffic,omitempty"`
}

type runtimeStatusTracker struct {
	mu       sync.RWMutex
	snapshot runtimeStatusSnapshot
}

func newRuntimeStatusTracker(profile runtime.ProtocolProfileID) *runtimeStatusTracker {
	startedAt := time.Now().UTC()
	return &runtimeStatusTracker{snapshot: runtimeStatusSnapshot{
		SchemaVersion:  runtimeStatusSchema,
		Running:        true,
		State:          string(nativeapp.StateConnecting),
		StartedAt:      &startedAt,
		Profile:        profile,
		AccessEvidence: "unknown",
	}}
}

func (tracker *runtimeStatusTracker) Snapshot() runtimeStatusSnapshot {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.snapshot
}

func runtimeStatusObserver(base nativeapp.ObserverFuncs, tracker *runtimeStatusTracker) nativeapp.ObserverFuncs {
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
			var sessionStartedAt *time.Time
			if !snapshot.SessionStartedAt.IsZero() {
				started := snapshot.SessionStartedAt.UTC()
				sessionStartedAt = &started
			}
			tracker.mu.Lock()
			tracker.snapshot.Traffic = &runtimeTrafficStatus{
				SessionStartedAt: sessionStartedAt, UploadBytes: snapshot.UploadBytes,
				DownloadBytes: snapshot.DownloadBytes, ActiveConnections: snapshot.ActiveConnections,
				TotalConnections: snapshot.TotalConnections,
			}
			tracker.mu.Unlock()
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

func ensureNoActiveRuntime(path string) error {
	_, err := queryRuntimeStatus(path)
	if err == nil {
		return errors.New("another native runtime is already active")
	}
	if errors.Is(err, errRuntimeNotRunning) {
		return nil
	}
	return err
}

func runStatus(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "status accepts no positional arguments")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	snapshot, err := queryRuntimeStatus(runtimeStatusPath(paths.Root))
	if errors.Is(err, errRuntimeNotRunning) {
		snapshot = runtimeStatusSnapshot{
			SchemaVersion: runtimeStatusSchema, State: "stopped", AccessEvidence: "unknown",
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "read runtime status: %v\n", err)
		return 1
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(snapshot); err != nil {
			fmt.Fprintf(stderr, "encode runtime status: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "running: %t\nstate: %s\n", snapshot.Running, snapshot.State)
		if snapshot.Running {
			fmt.Fprintf(stdout, "profile: %s\n", snapshot.Profile)
			if snapshot.StartedAt != nil {
				fmt.Fprintf(stdout, "started_at: %s\n", snapshot.StartedAt.UTC().Format(time.RFC3339))
			}
			fmt.Fprintf(stdout, "socks_listen: %s\naccess_evidence: %s\n", snapshot.SOCKSListen, snapshot.AccessEvidence)
			if snapshot.LastCommandFailure != "" {
				fmt.Fprintf(stdout, "last_command_failure: %s\n", snapshot.LastCommandFailure)
			}
			if snapshot.LastDataFailure != "" {
				fmt.Fprintf(stdout, "last_data_failure: %s\n", snapshot.LastDataFailure)
			}
			if snapshot.Traffic != nil {
				fmt.Fprintf(stdout, "traffic: upload=%d download=%d active=%d total=%d\n",
					snapshot.Traffic.UploadBytes, snapshot.Traffic.DownloadBytes,
					snapshot.Traffic.ActiveConnections, snapshot.Traffic.TotalConnections)
			}
		}
	}
	if !snapshot.Running {
		return 1
	}
	return 0
}
