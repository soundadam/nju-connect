package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
	"github.com/soundadam/soundconnect/internal/traffic"
)

type runtimeStatusTracker struct {
	mu       sync.RWMutex
	snapshot runtimecontrol.Snapshot
}

func newRuntimeStatusTracker(profile runtime.ProtocolProfileID) *runtimeStatusTracker {
	startedAt := time.Now().UTC()
	return &runtimeStatusTracker{snapshot: runtimecontrol.Snapshot{
		SchemaVersion:  runtimecontrol.SchemaVersion,
		Running:        true,
		State:          string(nativeapp.StateConnecting),
		StartedAt:      &startedAt,
		Profile:        profile,
		AccessEvidence: "unknown",
	}}
}

func (tracker *runtimeStatusTracker) Snapshot() runtimecontrol.Snapshot {
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.snapshot
}

func (tracker *runtimeStatusTracker) UpdateTraffic(snapshot nativeapp.TrafficSnapshot, sampledAt time.Time) {
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
func (tracker *runtimeStatusTracker) UpdateIngressTraffic(snapshot traffic.Snapshot, sampledAt time.Time) {
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

func runStatus(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect status", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	watch := flags.Bool("watch", false, "stream JSON status once per second")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "status accepts no positional arguments")
		return 2
	}
	if *watch && !*asJSON {
		fmt.Fprintln(stderr, "status --watch requires --json")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	for {
		snapshot, err := runtimecontrol.Current(paths.Root)
		if err != nil {
			fmt.Fprintf(stderr, "read runtime status: %v\n", err)
			return 1
		}
		if err := writeRuntimeStatus(stdout, snapshot, *asJSON); err != nil {
			fmt.Fprintf(stderr, "encode runtime status: %v\n", err)
			return 1
		}
		if !*watch {
			if !snapshot.Running {
				return 1
			}
			return 0
		}
		time.Sleep(time.Second)
	}
}

func writeRuntimeStatus(stdout io.Writer, snapshot runtimecontrol.Snapshot, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(snapshot)
	}
	fmt.Fprintf(stdout, "running: %t\nstate: %s\n", snapshot.Running, snapshot.State)
	if !snapshot.Running {
		return nil
	}
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
		fmt.Fprintf(stdout, "traffic: upload=%s download=%s active=%d total=%d\n",
			formatTotalBytes(snapshot.Traffic.UploadBytes), formatTotalBytes(snapshot.Traffic.DownloadBytes),
			snapshot.Traffic.ActiveConnections, snapshot.Traffic.TotalConnections)
	}
	return nil
}

func formatTotalBytes(bytes uint64) string {
	return fmt.Sprintf("%.1f KB", float64(bytes)/1_000)
}

func runDisconnect(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect disconnect", flag.ContinueOnError)
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "disconnect accepts no positional arguments")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	err = runtimecontrol.RequestDisconnect(runtimecontrol.Path(paths.Root))
	if errors.Is(err, runtimecontrol.ErrNotRunning) {
		fmt.Fprintln(stdout, "running: false")
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "disconnect runtime: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "stopping: true")
	return 0
}
