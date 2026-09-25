//go:build !linux && !darwin

package runtimecontrol

import (
	"errors"
	"os"
	"path/filepath"
)

type Server struct{}

func Path(string) string {
	return filepath.Join(os.TempDir(), "soundconnect-runtime", "runtime.sock")
}

func Serve(string, func() Snapshot) (*Server, error) {
	return nil, errors.New("runtime status is not supported on this platform")
}

func (server *Server) Close() error { return nil }

func (server *Server) SetStop(func()) {}

func Query(string) (Snapshot, error) {
	return Snapshot{}, errors.New("runtime status is not supported on this platform")
}

func RequestDisconnect(string) error {
	return errors.New("runtime control is not supported on this platform")
}
