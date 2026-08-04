//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/nativeapp"
	"github.com/soundadam/soundconnect/internal/runtime"
)

func TestRuntimeStatusTrackerRecordsExactTrafficSampleTime(t *testing.T) {
	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	sampledAt := time.Date(2026, time.August, 4, 8, 0, 0, 123_000_000, time.UTC)
	tracker.UpdateTraffic(nativeapp.TrafficSnapshot{
		UploadBytes: 12, DownloadBytes: 34, ActiveConnections: 2, TotalConnections: 3,
	}, sampledAt)

	traffic := tracker.Snapshot().Traffic
	if traffic == nil || traffic.SampledAtUnixMilli != sampledAt.UnixMilli() ||
		traffic.UploadBytes != 12 || traffic.DownloadBytes != 34 ||
		traffic.ActiveConnections != 2 || traffic.TotalConnections != 3 {
		t.Fatalf("traffic = %+v", traffic)
	}
}

func TestRuntimeStatusServerReportsSanitizedLiveStateAndCleansUp(t *testing.T) {
	path := runtimeStatusPath(t.TempDir())
	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	observer := runtimeStatusObserver(nativeapp.ObserverFuncs{}, tracker)
	observer.StateChanged(nativeapp.StateConnected)
	observer.SOCKSListening("127.0.0.1:1081")
	observer.AccessEvidence(true)

	server, err := startRuntimeStatusServer(path, tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("runtime socket mode = %v", info.Mode())
	}

	snapshot, err := queryRuntimeStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Running || snapshot.State != "connected" || snapshot.Profile != runtime.ProfileCommunityUTLSCompat ||
		snapshot.SOCKSListen != "127.0.0.1:1081" || snapshot.AccessEvidence != "available" {
		t.Fatalf("runtime status = %+v", snapshot)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime socket remains after close: %v", err)
	}
}

func TestDisconnectCommandStopsRuntimeThroughPrivateControlSocket(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml"), Credential: filepath.Join(root, "credential")}
	previous := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previous })

	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	server, err := startRuntimeStatusServer(runtimeStatusPath(root), tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var stopped atomic.Bool
	server.SetStop(func() { stopped.Store(true) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"disconnect"}, &stdout, &stderr); code != 0 || stdout.String() != "stopping: true\n" || stderr.Len() != 0 {
		t.Fatalf("disconnect exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !stopped.Load() {
		t.Fatal("runtime stop callback was not called")
	}
}

func TestStatusCommandSupportsTextJSONAndStoppedState(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml"), Credential: filepath.Join(root, "credential")}
	previous := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previous })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"status"}, &stdout, &stderr); code != 1 || stderr.Len() != 0 || stdout.String() != "running: false\nstate: stopped\n" {
		t.Fatalf("stopped exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	observer := runtimeStatusObserver(nativeapp.ObserverFuncs{}, tracker)
	observer.StateChanged(nativeapp.StateReconnecting)
	server, err := startRuntimeStatusServer(runtimeStatusPath(root), tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	stdout.Reset()
	if code := runStatus(nil, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "running: true\nstate: reconnecting\n") {
		t.Fatalf("live text exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := runStatus([]string{"--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("live JSON exit=%d stderr=%q", code, stderr.String())
	}
	var snapshot runtimeStatusSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil || !snapshot.Running || snapshot.State != "reconnecting" {
		t.Fatalf("JSON status=%+v err=%v", snapshot, err)
	}
}

func TestStatusWatchRequiresJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runStatus([]string{"--watch"}, &stdout, &stderr); code != 2 ||
		stdout.Len() != 0 || stderr.String() != "status --watch requires --json\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRuntimeStatusRejectsUnsafePathAndDuplicateServer(t *testing.T) {
	root := t.TempDir()
	unsafePath := filepath.Join(root, "runtime.sock")
	if err := os.WriteFile(unsafePath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	if _, err := startRuntimeStatusServer(unsafePath, tracker.Snapshot); err == nil {
		t.Fatal("regular file was accepted as a runtime socket")
	}

	path := runtimeStatusPath(t.TempDir())
	server, err := startRuntimeStatusServer(path, tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := ensureNoActiveRuntime(path); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("duplicate runtime check = %v", err)
	}
}
