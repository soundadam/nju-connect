// Package runtimecontrol is the private control channel of a running
// soundconnect runtime: the owner-only Unix socket that serves the sanitized
// status snapshot and accepts a disconnect request. Both the process that
// runs the dataplane (connect, _native-runtime) and the commands that
// observe or stop it (status, disconnect, configure) use it.
package runtimecontrol

import (
	"errors"
	"time"

	"github.com/soundadam/soundconnect/internal/runtime"
)

// SchemaVersion is the version of the status JSON written by `status --json`.
const SchemaVersion = 1

var (
	// ErrNotRunning means no live runtime answers on the control socket.
	ErrNotRunning = errors.New("native runtime is not running")
	// ErrAlreadyActive means another runtime already owns the control socket.
	ErrAlreadyActive = errors.New("another native runtime is already active")
)

// Traffic is the sanitized traffic sample inside a Snapshot.
type Traffic struct {
	SessionStartedAt   *time.Time `json:"session_started_at,omitempty"`
	SampledAtUnixMilli int64      `json:"sampled_at_unix_milli"`
	UploadBytes        uint64     `json:"upload_bytes"`
	DownloadBytes      uint64     `json:"download_bytes"`
	ActiveConnections  int64      `json:"active_connections"`
	TotalConnections   uint64     `json:"total_connections"`
}

// Snapshot is the status JSON contract shared with the macOS app.
type Snapshot struct {
	SchemaVersion      int                       `json:"schema_version"`
	Running            bool                      `json:"running"`
	State              string                    `json:"state"`
	StartedAt          *time.Time                `json:"started_at,omitempty"`
	Profile            runtime.ProtocolProfileID `json:"profile,omitempty"`
	SOCKSListen        string                    `json:"socks_listen,omitempty"`
	AccessEvidence     string                    `json:"access_evidence"`
	LastCommandFailure string                    `json:"last_command_failure,omitempty"`
	LastDataFailure    string                    `json:"last_data_failure,omitempty"`
	Traffic            *Traffic                  `json:"traffic,omitempty"`
}

// Stopped is the snapshot reported when nothing is running.
func Stopped() Snapshot {
	return Snapshot{SchemaVersion: SchemaVersion, State: "stopped", AccessEvidence: "unknown"}
}

// Current returns the live snapshot of the runtime that belongs to the
// configuration directory root, or Stopped when none is running.
func Current(root string) (Snapshot, error) {
	snapshot, err := Query(Path(root))
	if errors.Is(err, ErrNotRunning) {
		return Stopped(), nil
	}
	return snapshot, err
}

// EnsureNoActive fails with ErrAlreadyActive while a runtime answers on path.
func EnsureNoActive(path string) error {
	_, err := Query(path)
	if err == nil {
		return ErrAlreadyActive
	}
	if errors.Is(err, ErrNotRunning) {
		return nil
	}
	return err
}
