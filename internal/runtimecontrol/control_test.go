//go:build linux || darwin

package runtimecontrol

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/soundadam/nju-connect/internal/runtime"
)

func liveSnapshot() Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion, Running: true, State: "connected",
		Profile: runtime.ProfileCommunityUTLSCompat, SOCKSListen: "127.0.0.1:1080", AccessEvidence: "available",
	}
}

func TestServeQueryAndCloseRemovesSocket(t *testing.T) {
	path := Path(t.TempDir())
	server, err := Serve(path, liveSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket = %v, err = %v", info, err)
	}
	snapshot, err := Query(path)
	if err != nil || snapshot != liveSnapshot() {
		t.Fatalf("Query() = %+v, %v", snapshot, err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after Close: %v", err)
	}
	if _, err := Query(path); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Query() after Close = %v", err)
	}
}

func TestCurrentReportsStoppedWithoutRuntime(t *testing.T) {
	snapshot, err := Current(t.TempDir())
	if err != nil || snapshot != Stopped() || snapshot.Running {
		t.Fatalf("Current() = %+v, %v", snapshot, err)
	}
}

func TestEnsureNoActiveAndDuplicateServe(t *testing.T) {
	root := t.TempDir()
	if err := EnsureNoActive(Path(root)); err != nil {
		t.Fatalf("EnsureNoActive() without runtime = %v", err)
	}
	server, err := Serve(Path(root), liveSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := EnsureNoActive(Path(root)); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("EnsureNoActive() = %v", err)
	}
	if _, err := Serve(Path(root), liveSnapshot); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("second Serve() = %v", err)
	}
}

func TestRequestDisconnectRunsStopCallback(t *testing.T) {
	path := Path(t.TempDir())
	if err := RequestDisconnect(path); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("RequestDisconnect() without runtime = %v", err)
	}
	server, err := Serve(path, liveSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := RequestDisconnect(path); err == nil {
		t.Fatal("disconnect accepted before a stop callback was installed")
	}
	var stopped atomic.Bool
	server.SetStop(func() { stopped.Store(true) })
	if err := RequestDisconnect(path); err != nil {
		t.Fatal(err)
	}
	if !stopped.Load() {
		t.Fatal("stop callback was not called")
	}
}

func TestQueryRejectsInvalidSnapshots(t *testing.T) {
	for name, mutate := range map[string]func(*Snapshot){
		"schema":        func(s *Snapshot) { s.SchemaVersion = 2 },
		"not running":   func(s *Snapshot) { s.Running = false },
		"state":         func(s *Snapshot) { s.State = "stopped" },
		"profile":       func(s *Snapshot) { s.Profile = "unknown" },
		"evidence":      func(s *Snapshot) { s.AccessEvidence = "maybe" },
		"remote socks":  func(s *Snapshot) { s.SOCKSListen = "0.0.0.0:1080" },
		"command stage": func(s *Snapshot) { s.LastCommandFailure = "secret-token" },
		"data stage":    func(s *Snapshot) { s.LastDataFailure = "secret-token" },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := liveSnapshot()
			mutate(&snapshot)
			path := Path(t.TempDir())
			server, err := Serve(path, func() Snapshot { return snapshot })
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			if _, err := Query(path); err == nil || errors.Is(err, ErrNotRunning) {
				t.Fatalf("Query() = %v", err)
			}
		})
	}
}

func TestServeRejectsNonSocketPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Serve(path, liveSnapshot); err == nil {
		t.Fatal("regular file was accepted as a runtime socket")
	}
}
