//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
)

type runtimeStatusServer struct{}

func runtimeStatusPath(string) string {
	return filepath.Join(os.TempDir(), "soundconnect-runtime", "runtime.sock")
}

func startRuntimeStatusServer(string, func() runtimeStatusSnapshot) (*runtimeStatusServer, error) {
	return nil, errors.New("runtime status is not supported on this platform")
}

func (server *runtimeStatusServer) Close() error { return nil }

func (server *runtimeStatusServer) SetStop(func()) {}

func queryRuntimeStatus(string) (runtimeStatusSnapshot, error) {
	return runtimeStatusSnapshot{}, errors.New("runtime status is not supported on this platform")
}

func requestRuntimeDisconnect(string) error {
	return errors.New("runtime control is not supported on this platform")
}
