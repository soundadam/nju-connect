package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/core"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
	"github.com/soundadam/nju-connect/internal/sessiontoken"
)

const (
	backgroundHandoffFD      = 3
	backgroundReadyFD        = 4
	maximumBackgroundHandoff = 16 << 10
	backgroundStartupTimeout = 45 * time.Second
)

type backgroundHandoff struct {
	Settings      config.Config                   `json:"settings"`
	Plan          core.DataplanePlan              `json:"plan"`
	Token         sessiontoken.NativeGatewayToken `json:"token"`
	NativeProfile runtime.ProtocolProfileID       `json:"native_profile"`
	StatusPath    string                          `json:"status_path"`
}

// BackgroundRuntimeCommand is the hidden command that runs a detached
// EasyConnect runtime. The parent passes the handoff on fd 3 and waits for
// one readiness byte on fd 4.
const BackgroundRuntimeCommand = "_native-runtime"

// StartBackground re-executes this binary as a detached runtime, hands it the
// authenticated session over a private pipe, and waits until it reports that
// it is connected. It is the production Deps.StartBackground.
func StartBackground(sessionConfig nativeapp.SessionConfig, logPath string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, errors.New("resolve nju-connect executable")
	}
	logFile, err := openPrivateBackgroundLog(logPath)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, errors.New("open background standard input")
	}
	defer devNull.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return 0, errors.New("create private background handoff")
	}
	defer reader.Close()
	defer writer.Close()
	readyReader, readyWriter, err := os.Pipe()
	if err != nil {
		return 0, errors.New("create background readiness handoff")
	}
	defer readyReader.Close()
	defer readyWriter.Close()

	command := exec.Command(executable, BackgroundRuntimeCommand)
	command.Stdin = devNull
	command.Stdout = logFile
	command.Stderr = logFile
	command.ExtraFiles = []*os.File{reader, readyWriter}
	if err := configureBackgroundProcess(command); err != nil {
		return 0, err
	}
	if err := command.Start(); err != nil {
		return 0, errors.New("start background native runtime")
	}
	_ = reader.Close()
	_ = readyWriter.Close()

	handoff := backgroundHandoff{
		Settings:      sessionConfig.Settings,
		Plan:          sessionConfig.Plan,
		Token:         append(sessiontoken.NativeGatewayToken(nil), sessionConfig.NativeGatewayToken...),
		NativeProfile: sessionConfig.NativeProfile,
		StatusPath:    runtimecontrol.Path(filepath.Dir(logPath)),
	}
	writeErr := writeBackgroundHandoff(writer, &handoff)
	credential.Clear(handoff.Token)
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 0, errors.New("transfer private background handoff")
	}
	if err := readyReader.SetReadDeadline(time.Now().Add(backgroundStartupTimeout)); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 0, errors.New("prepare background readiness check")
	}
	var ready [1]byte
	if _, err := io.ReadFull(readyReader, ready[:]); err != nil || ready[0] != 1 {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 0, errors.New("background native runtime did not initialize")
	}
	pid := command.Process.Pid
	if err := command.Process.Release(); err != nil {
		_ = command.Process.Kill()
		return 0, errors.New("release background native runtime")
	}
	return pid, nil
}

// BackgroundFiles opens the handoff and readiness descriptors a detached
// runtime inherits. Run by hand, fds 3 and 4 may belong to the Go runtime
// itself, so they are adopted only when both are inherited pipes.
func BackgroundFiles() (handoff, ready *os.File, err error) {
	if !inheritedPipe(backgroundHandoffFD) || !inheritedPipe(backgroundReadyFD) {
		return nil, nil, errors.New("background runtime: private handoff is unavailable")
	}
	handoff = os.NewFile(backgroundHandoffFD, "nju-connect-background-handoff")
	ready = os.NewFile(backgroundReadyFD, "nju-connect-background-ready")
	if handoff == nil || ready == nil {
		if handoff != nil {
			_ = handoff.Close()
		}
		if ready != nil {
			_ = ready.Close()
		}
		return nil, nil, errors.New("background runtime: private handoff is unavailable")
	}
	return handoff, ready, nil
}

// RunBackground is the detached runtime. It reads the session from handoff,
// writes one byte to ready once connected, publishes status on the control
// socket, and runs until ctx ends or `disconnect` asks it to stop.
//
// The parent stops waiting as soon as ready closes, so on failure the caller
// closes ready only after it has logged the returned error.
func RunBackground(ctx context.Context, deps Deps, handoffReader io.ReadCloser, ready io.WriteCloser, events ConnectEvents) error {
	handoff, err := readBackgroundHandoff(handoffReader)
	_ = handoffReader.Close()
	if err != nil {
		return errors.New("background runtime: private handoff is invalid")
	}
	defer credential.Clear(handoff.Token)

	statusTracker := NewRuntimeStatusTracker(handoff.NativeProfile)
	observer := RuntimeStatusObserver(events.Runtime, statusTracker)
	stateObserver := observer.OnState
	var readyOnce sync.Once
	observer.OnState = func(state nativeapp.State) {
		stateObserver(state)
		if state == nativeapp.StateConnected {
			readyOnce.Do(func() {
				if _, err := ready.Write([]byte{1}); err != nil {
					fmt.Fprintln(deps.diagnostics(), "background runtime: readiness handoff failed")
				}
				_ = ready.Close()
			})
		}
	}
	application, err := deps.EasyConnectSession(nativeapp.SessionConfig{
		Settings:           handoff.Settings,
		Plan:               handoff.Plan,
		NativeGatewayToken: handoff.Token,
		NativeProfile:      handoff.NativeProfile,
		Observer:           observer,
	})
	if err != nil || application == nil {
		return errors.New("background runtime: initialize_failed")
	}
	defer application.Close()
	statusServer, err := runtimecontrol.Serve(handoff.StatusPath, func() runtimecontrol.Snapshot {
		statusTracker.UpdateTraffic(application.Traffic(), time.Now())
		return statusTracker.Snapshot()
	})
	if err != nil {
		return errors.New("background runtime: status_control_failed")
	}
	defer statusServer.Close()
	runtimeContext, stop := context.WithCancel(ctx)
	defer stop()
	statusServer.SetStop(stop)
	events.started(application.Profile())
	return runResult(runtimeContext, application.Run(runtimeContext))
}

func writeBackgroundHandoff(writer io.Writer, handoff *backgroundHandoff) error {
	if writer == nil || handoff == nil || len(handoff.Token) != sessiontoken.NativeGatewayTokenSize {
		return errors.New("invalid background handoff")
	}
	payload, err := json.Marshal(handoff)
	if err != nil {
		return errors.New("encode background handoff")
	}
	defer clear(payload)
	if len(payload) > maximumBackgroundHandoff {
		return errors.New("background handoff is too large")
	}
	written, err := writer.Write(payload)
	if err != nil || written != len(payload) {
		return errors.New("write background handoff")
	}
	return nil
}

func readBackgroundHandoff(reader io.Reader) (backgroundHandoff, error) {
	if reader == nil {
		return backgroundHandoff{}, errors.New("background handoff reader is required")
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maximumBackgroundHandoff+1))
	if err != nil || len(payload) == 0 || len(payload) > maximumBackgroundHandoff {
		clear(payload)
		return backgroundHandoff{}, errors.New("read background handoff")
	}
	defer clear(payload)
	var handoff backgroundHandoff
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&handoff); err != nil {
		credential.Clear(handoff.Token)
		return backgroundHandoff{}, errors.New("decode background handoff")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		credential.Clear(handoff.Token)
		return backgroundHandoff{}, errors.New("decode background handoff")
	}
	if len(handoff.Token) != sessiontoken.NativeGatewayTokenSize || handoff.NativeProfile != runtime.ProfileCommunityUTLSCompat ||
		filepath.Ext(handoff.StatusPath) != ".sock" || !filepath.IsAbs(handoff.StatusPath) {
		credential.Clear(handoff.Token)
		return backgroundHandoff{}, errors.New("validate background handoff")
	}
	return handoff, nil
}

func openPrivateBackgroundLog(path string) (*os.File, error) {
	if path == "" {
		return nil, errors.New("background log path is required")
	}
	var expected os.FileInfo
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("background log must be an owner-only regular file")
		}
		expected = info
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect background log")
	}
	flags := os.O_APPEND | os.O_WRONLY
	if expected == nil {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, errors.New("open background log")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || (expected != nil && !os.SameFile(expected, info)) {
		_ = file.Close()
		return nil, errors.New("validate background log")
	}
	return file, nil
}
