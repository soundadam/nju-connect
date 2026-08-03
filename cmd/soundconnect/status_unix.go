//go:build linux || darwin

package main

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
)

const maximumRuntimeStatus = 16 << 10

func runtimeStatusPath(root string) string {
	digest := sha256.Sum256([]byte(filepath.Clean(root)))
	directory := filepath.Join(os.TempDir(), fmt.Sprintf("soundconnect-runtime-%d", os.Geteuid()))
	return filepath.Join(directory, fmt.Sprintf("%x.sock", digest[:12]))
}

type runtimeStatusServer struct {
	listener *net.UnixListener
	path     string
	fileInfo os.FileInfo
	snapshot func() runtimeStatusSnapshot
	done     chan struct{}
	once     sync.Once
}

func startRuntimeStatusServer(path string, snapshot func() runtimeStatusSnapshot) (*runtimeStatusServer, error) {
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
	server := &runtimeStatusServer{
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
		return errors.New("another native runtime is already active")
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

func (server *runtimeStatusServer) serve() {
	defer close(server.done)
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			return
		}
		_ = connection.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_ = json.NewEncoder(connection).Encode(server.snapshot())
		_ = connection.Close()
	}
}

func (server *runtimeStatusServer) Close() error {
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

func queryRuntimeStatus(path string) (runtimeStatusSnapshot, error) {
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return runtimeStatusSnapshot{}, errRuntimeNotRunning
	}
	if err != nil {
		return runtimeStatusSnapshot{}, errors.New("inspect runtime status directory")
	}
	if err := validateRuntimeDirectory(directoryInfo); err != nil {
		return runtimeStatusSnapshot{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return runtimeStatusSnapshot{}, errRuntimeNotRunning
	}
	if err != nil {
		return runtimeStatusSnapshot{}, errors.New("inspect runtime status socket")
	}
	if err := validateRuntimeSocket(info); err != nil {
		return runtimeStatusSnapshot{}, err
	}
	connection, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return runtimeStatusSnapshot{}, errRuntimeNotRunning
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	payload, err := io.ReadAll(io.LimitReader(connection, maximumRuntimeStatus+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumRuntimeStatus {
		return runtimeStatusSnapshot{}, errors.New("runtime status response is invalid")
	}
	var snapshot runtimeStatusSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return runtimeStatusSnapshot{}, errors.New("runtime status response is invalid")
	}
	if !validRuntimeStatusSnapshot(snapshot) {
		return runtimeStatusSnapshot{}, errors.New("runtime status response is invalid")
	}
	return snapshot, nil
}

func validRuntimeStatusSnapshot(snapshot runtimeStatusSnapshot) bool {
	if snapshot.SchemaVersion != runtimeStatusSchema || !snapshot.Running || !validRuntimeStatusState(snapshot.State) ||
		snapshot.Profile != "community-utls" || !validAccessEvidence(snapshot.AccessEvidence) ||
		!validStatusSOCKSListen(snapshot.SOCKSListen) || !validCommandFailure(snapshot.LastCommandFailure) ||
		!validDataFailure(snapshot.LastDataFailure) {
		return false
	}
	return true
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
	return state == string(nativeappStateConnecting) || state == string(nativeappStateConnected) || state == string(nativeappStateReconnecting)
}

type runtimeStatusState string

const (
	nativeappStateConnecting   runtimeStatusState = "connecting"
	nativeappStateConnected    runtimeStatusState = "connected"
	nativeappStateReconnecting runtimeStatusState = "reconnecting"
)
