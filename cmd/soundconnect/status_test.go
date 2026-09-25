//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
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
	path := runtimecontrol.Path(t.TempDir())
	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	observer := runtimeStatusObserver(nativeapp.ObserverFuncs{}, tracker)
	observer.StateChanged(nativeapp.StateConnected)
	observer.SOCKSListening("127.0.0.1:1081")
	observer.AccessEvidence(true)

	server, err := runtimecontrol.Serve(path, tracker.Snapshot)
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

	snapshot, err := runtimecontrol.Query(path)
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
	server, err := runtimecontrol.Serve(runtimecontrol.Path(root), tracker.Snapshot)
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
	server, err := runtimecontrol.Serve(runtimecontrol.Path(root), tracker.Snapshot)
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
	var snapshot runtimecontrol.Snapshot
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

func TestTextStatusFormatsTrafficTotalsInKB(t *testing.T) {
	snapshot := runtimecontrol.Snapshot{
		Running: true, State: "connected", Traffic: &runtimecontrol.Traffic{
			UploadBytes: 1_500, DownloadBytes: 2_750,
		},
	}
	var output bytes.Buffer
	if err := writeRuntimeStatus(&output, snapshot, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "traffic: upload=1.5 KB download=2.8 KB") {
		t.Fatalf("text status = %q", output.String())
	}
}

func TestConnectGuidesUserWhenRuntimeIsAlreadyActive(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml"), Credential: filepath.Join(root, "credential")}
	previous := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previous })

	tracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	server, err := runtimecontrol.Serve(runtimecontrol.Path(root), tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runNativeConnectContext(context.Background(), nil, &stdout, &stderr,
		func(nativeapp.SessionConfig) (nativeApplicationSession, error) {
			t.Fatal("session factory was reached while a runtime is active")
			return nil, nil
		}, nil)
	if code != 1 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "already running") ||
		!strings.Contains(stderr.String(), `"soundconnect disconnect"`) ||
		!strings.Contains(stderr.String(), `"soundconnect status"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRuntimeStatusAcceptsATrustTCPProfile(t *testing.T) {
	path := runtimecontrol.Path(t.TempDir())
	tracker := newRuntimeStatusTracker(runtime.ProfileATrustTCP)
	observer := runtimeStatusObserver(nativeapp.ObserverFuncs{}, tracker)
	observer.StateChanged(nativeapp.StateConnected)
	observer.SOCKSListening(config.DefaultSOCKSListen)
	observer.AccessEvidence(true)

	server, err := runtimecontrol.Serve(path, tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	snapshot, err := runtimecontrol.Query(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile != runtime.ProfileATrustTCP || snapshot.SOCKSListen != config.DefaultSOCKSListen || snapshot.AccessEvidence != "available" {
		t.Fatalf("aTrust runtime status = %+v", snapshot)
	}
}
