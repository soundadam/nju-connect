//go:build linux || darwin

package runtimecontrol

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/soundadam/soundconnect/internal/runtime"
)

const maximumRuntimeStatus = 16 << 10
const maximumRuntimeControl = 1024

type runtimeControlRequest struct {
	Command string `json:"command"`
}

type runtimeControlResponse struct {
	OK bool `json:"ok"`
}

// Path is the control socket of the runtime that belongs to the configuration
// directory root. It lives in a private per-user directory under TMPDIR.
func Path(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("soundconnect-runtime-%d", os.Geteuid()))
	return filepath.Join(directory, fmt.Sprintf("%x.sock", digest[:12]))
}

// Server serves the control socket for one running runtime.
type Server struct {
	listener  *net.UnixListener
	path      string
	fileInfo  os.FileInfo
	snapshot  func() Snapshot
	done      chan struct{}
	once      sync.Once
	controlMu sync.RWMutex
	stop      func()
}

// SetStop installs the callback run when a client requests a disconnect.
func (server *Server) SetStop(stop func()) {
	if server == nil {
		return
	}
	server.controlMu.Lock()
	server.stop = stop
	server.controlMu.Unlock()
}

// Serve listens on path and answers every client with snapshot().
func Serve(path string, snapshot func() Snapshot) (*Server, error) {
	if snapshot == nil {
		return nil, errors.New("runtime status source is unavailable")
	}
	if err := ensurePrivateRuntimeDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := prepareRuntimeSocket(path); err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, errors.New("listen on private runtime status socket")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, errors.New("protect runtime status socket")
	}
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, errors.New("inspect runtime status socket")
	}
	server := &Server{
		listener: listener, path: path, fileInfo: info, snapshot: snapshot, done: make(chan struct{}),
	}
	go server.serve()
	return server, nil
}

func ensurePrivateRuntimeDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.New("create private runtime status directory")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("inspect runtime status directory")
	}
	return validateRuntimeDirectory(info)
}

func validateRuntimeDirectory(info os.FileInfo) error {
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("runtime status directory is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("runtime status directory has the wrong owner")
	}
	return nil
}

func prepareRuntimeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("inspect runtime status socket")
	}
	if err := validateRuntimeSocket(info); err != nil {
		return err
	}
	connection, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return ErrAlreadyActive
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(current, info) {
		return errors.New("runtime status socket changed while checking liveness")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("remove stale runtime status socket")
	}
	return nil
}

func validateRuntimeSocket(info os.FileInfo) error {
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("runtime status path is not a socket")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("runtime status socket permissions are unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("runtime status socket has the wrong owner")
	}
	return nil
}

func (server *Server) serve() {
	defer close(server.done)
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			return
		}
		_ = connection.SetReadDeadline(time.Now().Add(25 * time.Millisecond))
		var control runtimeControlRequest
		readErr := json.NewDecoder(io.LimitReader(connection, maximumRuntimeControl)).Decode(&control)
		if readErr == nil && control.Command == "disconnect" {
			server.controlMu.RLock()
			stop := server.stop
			server.controlMu.RUnlock()
			_ = connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if stop != nil {
				_ = json.NewEncoder(connection).Encode(runtimeControlResponse{OK: true})
				stop()
			} else {
				_ = json.NewEncoder(connection).Encode(runtimeControlResponse{OK: false})
			}
			_ = connection.Close()
			continue
		}
		if readErr == nil {
			_ = connection.Close()
			continue
		}
		_ = connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = json.NewEncoder(connection).Encode(server.snapshot())
		_ = connection.Close()
	}
}

// RequestDisconnect asks the runtime on path to stop. It returns
// ErrNotRunning when nothing answers.
func RequestDisconnect(path string) error {
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotRunning
	}
	if err != nil || validateRuntimeDirectory(directoryInfo) != nil {
		return errors.New("inspect runtime status directory")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotRunning
	}
	if err != nil || validateRuntimeSocket(info) != nil {
		return errors.New("inspect runtime status socket")
	}
	connection, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return ErrNotRunning
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(connection).Encode(runtimeControlRequest{Command: "disconnect"}); err != nil {
		return errors.New("send runtime disconnect request")
	}
	var response runtimeControlResponse
	if err := json.NewDecoder(io.LimitReader(connection, maximumRuntimeControl)).Decode(&response); err != nil || !response.OK {
		return errors.New("runtime rejected disconnect request")
	}
	return nil
}

// Close stops serving and removes the socket it created.
func (server *Server) Close() error {
	if server == nil {
		return nil
	}
	var closeErr error
	server.once.Do(func() {
		closeErr = server.listener.Close()
		<-server.done
		if current, err := os.Lstat(server.path); err == nil && os.SameFile(current, server.fileInfo) {
			_ = os.Remove(server.path)
		}
	})
	return closeErr
}

// Query reads and validates the snapshot of the runtime on path. It returns
// ErrNotRunning when nothing answers.
func Query(path string) (Snapshot, error) {
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, ErrNotRunning
	}
	if err != nil {
		return Snapshot{}, errors.New("inspect runtime status directory")
	}
	if err := validateRuntimeDirectory(directoryInfo); err != nil {
		return Snapshot{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, ErrNotRunning
	}
	if err != nil {
		return Snapshot{}, errors.New("inspect runtime status socket")
	}
	if err := validateRuntimeSocket(info); err != nil {
		return Snapshot{}, err
	}
	connection, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return Snapshot{}, ErrNotRunning
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload, err := io.ReadAll(io.LimitReader(connection, maximumRuntimeStatus+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumRuntimeStatus {
		return Snapshot{}, errors.New("runtime status response is invalid")
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return Snapshot{}, errors.New("runtime status response is invalid")
	}
	if !validRuntimeStatusSnapshot(snapshot) {
		return Snapshot{}, errors.New("runtime status response is invalid")
	}
	return snapshot, nil
}

func validRuntimeStatusSnapshot(snapshot Snapshot) bool {
	if snapshot.SchemaVersion != SchemaVersion || !snapshot.Running || !validRuntimeStatusState(snapshot.State) ||
		!validRuntimeStatusProfile(snapshot.Profile) || !validAccessEvidence(snapshot.AccessEvidence) ||
		!validStatusSOCKSListen(snapshot.SOCKSListen) || !validCommandFailure(snapshot.LastCommandFailure) ||
		!validDataFailure(snapshot.LastDataFailure) {
		return false
	}
	return true
}

func validRuntimeStatusProfile(profile runtime.ProtocolProfileID) bool {
	return profile == runtime.ProfileCommunityUTLSCompat || profile == runtime.ProfileATrustTCP
}

func validAccessEvidence(value string) bool {
	return value == "unknown" || value == "available" || value == "unavailable"
}

func validStatusSOCKSListen(address string) bool {
	if address == "" {
		return true
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return false
	}
	parsed, err := netip.ParseAddr(host)
	return err == nil && parsed.IsLoopback()
}

func validCommandFailure(stage string) bool {
	switch stage {
	case "", "upstream_connect_failed", "protocol_tls_handshake_failed", "protocol_tls_certificate_failed",
		"protocol_preface_failed", "send_ip_write_failed", "send_ip_read_failed", "send_ip_rejected":
		return true
	default:
		return false
	}
}

func validDataFailure(stage string) bool {
	switch stage {
	case "", "rx_handshake_failed", "tx_handshake_failed", "rx_stream_closed", "rx_invalid_ipv4", "tx_stream_closed":
		return true
	default:
		return false
	}
}

func validRuntimeStatusState(state string) bool {
	return state == "connecting" || state == "connected" || state == "reconnecting"
}
