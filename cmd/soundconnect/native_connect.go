package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/dial"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

const nativeUpstreamPreflightTimeout = 3 * time.Second

type nativeApplicationSession interface {
	Run(context.Context) error
	Close() error
	Traffic() nativeapp.TrafficSnapshot
	Profile() runtime.ProtocolProfileMetadata
}

type nativeSessionFactory func(nativeapp.SessionConfig) (nativeApplicationSession, error)
type nativeBackgroundStarter func(nativeapp.SessionConfig, string) (int, error)

func newProductionNativeSession(sessionConfig nativeapp.SessionConfig) (nativeApplicationSession, error) {
	return nativeapp.NewSession(sessionConfig)
}

func runNativeConnect(arguments []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runNativeConnectContext(ctx, arguments, stdout, stderr, newProductionNativeSession, startProductionNativeBackground)
}

func runNativeConnectContext(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	newSession nativeSessionFactory,
	startBackground nativeBackgroundStarter,
) int {
	flags := flag.NewFlagSet("soundconnect connect", flag.ContinueOnError)
	background := flags.Bool("background", false, "continue the native runtime as a detached process after authentication")
	verificationCodeStdin := flags.Bool("verification-code-stdin", false, "read the verification code from standard input without requiring a terminal")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "connect accepts no positional arguments")
		return 2
	}
	if !*background && newSession == nil {
		fmt.Fprintln(stderr, "prepare native runtime: session factory is unavailable")
		return 1
	}
	if *background && startBackground == nil {
		fmt.Fprintln(stderr, "prepare native runtime: background starter is unavailable")
		return 1
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	if err := ensureNoActiveRuntime(runtimeStatusPath(paths.Root)); err != nil {
		if errors.Is(err, errRuntimeAlreadyActive) {
			fmt.Fprintln(stderr, `soundconnect is already running; run "soundconnect status" to inspect it or "soundconnect disconnect" to stop it`)
			return 1
		}
		fmt.Fprintf(stderr, "prepare runtime status: %v\n", err)
		return 1
	}
	configured, err := config.Load(paths.Config)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}

	preflightContext, cancelPreflight := context.WithTimeout(ctx, nativeUpstreamPreflightTimeout)
	err = dial.ProbeUpstream(preflightContext, configured.UpstreamProxy)
	cancelPreflight()
	if err != nil {
		fmt.Fprintf(stderr, "upstream preflight: %v\n", err)
		return 1
	}

	passwordStore, _, err := commandCredentialStore(paths)
	if err != nil {
		fmt.Fprintf(stderr, "open credential: %v\n", err)
		return 1
	}
	password, err := passwordStore.Get()
	if err != nil {
		fmt.Fprintf(stderr, "read credential: %v\n", err)
		return 1
	}
	session, err := func() (*gatewayauth.Session, error) {
		defer credential.Clear(password)
		return authenticateAttendedGateway(ctx, configured, password, func() ([]byte, error) {
			return promptVerificationCode(ctx, os.Stdin, stderr, *verificationCodeStdin)
		})
	}()
	password = nil
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		fmt.Fprintf(stderr, "authenticate gateway: %v\n", err)
		return 1
	}
	defer session.Close()
	fmt.Fprintln(stdout, "authentication: accepted")

	bootstrap, err := session.ProbeBootstrap(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "initialize gateway session: %v\n", err)
		return 1
	}
	plan, err := core.BuildDataplanePlan(session.State(), bootstrap)
	if err != nil {
		fmt.Fprintf(stderr, "model native dataplane: %v\n", err)
		return 1
	}

	var application nativeApplicationSession
	var backgroundPID int
	statusTracker := newRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	err = session.WithNativeGatewayToken(func(token sessiontoken.NativeGatewayToken) error {
		sessionConfig := nativeapp.SessionConfig{
			Settings:               configured,
			Plan:                   plan,
			NativeGatewayToken:     token,
			NativeProfile:          runtime.ProfileCommunityUTLSCompat,
			TrafficPublishInterval: time.Second,
		}
		if *background {
			var startErr error
			backgroundPID, startErr = startBackground(sessionConfig, filepath.Join(paths.Root, "runtime.log"))
			return startErr
		}
		var buildErr error
		sessionConfig.Observer = runtimeStatusObserver(nativeCLIObserver(stdout), statusTracker)
		application, buildErr = newSession(sessionConfig)
		return buildErr
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare native runtime: %v\n", err)
		return 1
	}
	if *background {
		fmt.Fprintf(stdout, "background: pid=%d log=%s\n", backgroundPID, filepath.Join(paths.Root, "runtime.log"))
		return 0
	}
	if application == nil {
		fmt.Fprintln(stderr, "prepare native runtime: session factory returned no session")
		return 1
	}
	defer application.Close()
	statusServer, err := startRuntimeStatusServer(runtimeStatusPath(paths.Root), func() runtimeStatusSnapshot {
		statusTracker.UpdateTraffic(application.Traffic(), time.Now())
		return statusTracker.Snapshot()
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare runtime status: %v\n", err)
		return 1
	}
	defer statusServer.Close()
	profile := application.Profile()
	fmt.Fprintf(stdout, "native-profile: %s\n", profile.ID)
	fmt.Fprintf(stdout, "native-evidence: %s\n", profile.Evidence)
	fmt.Fprintf(stdout, "native-security: encrypted=%t peer_verified=%t\n", profile.Security.Encrypted, profile.Security.PeerVerified)

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	statusServer.SetStop(cancelRuntime)
	err = application.Run(runtimeContext)
	return reportNativeRunResult(runtimeContext, err, stderr)
}

func reportNativeRunResult(ctx context.Context, err error, stderr io.Writer) int {
	if err == nil || (ctx.Err() != nil && errors.Is(err, context.Canceled)) {
		return 0
	}
	if errors.Is(err, runtime.ErrRenewalRequired) {
		fmt.Fprintln(stderr, `renewal_required: run "soundconnect connect" to sign in again`)
		return 1
	}
	var failure *runtime.TransportFailure
	if errors.As(err, &failure) {
		fmt.Fprintf(stderr, "native transport: %s\n", failure.Error())
		return 1
	}
	fmt.Fprintln(stderr, "native transport: runtime_stopped")
	return 1
}

func authenticateAttendedGateway(
	ctx context.Context,
	configured config.Config,
	password []byte,
	readVerificationCode func() ([]byte, error),
) (*gatewayauth.Session, error) {
	client, err := gatewayauth.New(gatewayauth.Options{
		Server:        configured.Server,
		TLSInsecure:   configured.TLSInsecure,
		UpstreamProxy: configured.UpstreamProxy,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare authentication: %w", err)
	}
	defer client.Close()

	result, err := client.AuthenticatePassword(ctx, configured.Username, password)
	if err != nil {
		return nil, fmt.Errorf("password authentication: %w", err)
	}
	if result.NeedsSMS() {
		if err := client.PrepareSMS(ctx); err != nil {
			return nil, err
		}
		if readVerificationCode == nil {
			return nil, errors.New("verification code input is required")
		}
		code, err := readVerificationCode()
		if err != nil {
			return nil, err
		}
		result, err = func() (gatewayauth.Result, error) {
			defer credential.Clear(code)
			return client.AuthenticateSMS(ctx, code)
		}()
		if err != nil {
			return nil, fmt.Errorf("verification code authentication: %w", err)
		}
	}
	if !result.Accepted() {
		if result.NextService != "" {
			return nil, errors.New("gateway requires an unsupported authentication step")
		}
		return nil, fmt.Errorf("gateway rejected authentication with code %d", result.Code)
	}
	session, err := client.TakeSession()
	if err != nil {
		return nil, err
	}
	return session, nil
}

func nativeCLIObserver(output io.Writer) nativeapp.ObserverFuncs {
	return nativeapp.ObserverFuncs{
		OnState: func(state nativeapp.State) {
			fmt.Fprintf(output, "state: %s\n", state)
		},
		OnCommandFailure: func(failure nativeapp.CommandFailure) {
			fmt.Fprintf(output, "command: at=%s attempt=%d stage=%s\n",
				failure.At.UTC().Format(time.RFC3339Nano), failure.Attempt, failure.Stage)
		},
		OnDataFailure: func(stage nativeapp.DataFailureStage) {
			fmt.Fprintf(output, "data: stage=%s\n", stage)
		},
		OnSOCKSListen: func(address string) {
			fmt.Fprintf(output, "socks: %s\n", address)
		},
		OnTraffic: func(snapshot nativeapp.TrafficSnapshot) {
			fmt.Fprintf(output, "traffic: upload=%s download=%s active=%d total=%d\n",
				formatTotalBytes(snapshot.UploadBytes), formatTotalBytes(snapshot.DownloadBytes),
				snapshot.ActiveConnections, snapshot.TotalConnections)
		},
		OnAccessEvidence: func(available bool) {
			fmt.Fprintf(output, "access: available=%t\n", available)
		},
	}
}
