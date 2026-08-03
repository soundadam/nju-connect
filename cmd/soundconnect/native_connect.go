package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/dial"
	"github.com/soundadam/soundconnect/internal/gatewayauth"
	"github.com/soundadam/soundconnect/internal/nativeapp"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

const nativeUpstreamPreflightTimeout = 3 * time.Second

type nativeApplicationSession interface {
	Run(context.Context) error
	Close() error
}

type nativeSessionFactory func(nativeapp.SessionConfig) (nativeApplicationSession, error)

func newProductionNativeSession(sessionConfig nativeapp.SessionConfig) (nativeApplicationSession, error) {
	return nativeapp.NewSession(sessionConfig)
}

func runNativeConnect(arguments []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runNativeConnectContext(ctx, arguments, stdout, stderr, newProductionNativeSession)
}

func runNativeConnectContext(
	ctx context.Context,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	newSession nativeSessionFactory,
) int {
	flags := flag.NewFlagSet("native-connect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worktree := flags.String("worktree", ".", "soundconnect working tree")
	resolveIP := flags.String("resolve-ip", "", "development-only numeric gateway address override")
	accessProbeURL := flags.String("access-probe-url", "", "disclosed campus HTTP(S) URL used for HEAD evidence")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "native-connect accepts no positional arguments")
		return 2
	}
	if newSession == nil {
		fmt.Fprintln(stderr, "prepare native runtime: session factory is unavailable")
		return 1
	}

	paths, err := config.LocalPaths(*worktree)
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
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

	passwordStore, err := credential.NewFileStore(paths.Credential, true)
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
		return authenticateAttendedGateway(ctx, configured, *resolveIP, password, func() ([]byte, error) {
			return promptVerificationCode(os.Stdin, stderr)
		})
	}()
	password = nil
	if err != nil {
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
	err = session.WithNativeGatewayToken(func(token sessiontoken.NativeGatewayToken) error {
		var buildErr error
		application, buildErr = newSession(nativeapp.SessionConfig{
			Settings:           configured,
			Plan:               plan,
			NativeGatewayToken: token,
			ResolveGatewayIP:   *resolveIP,
			AccessProbeURL:     *accessProbeURL,
			Observer:           nativeCLIObserver(stdout),
		})
		return buildErr
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare native runtime: %v\n", err)
		return 1
	}
	if application == nil {
		fmt.Fprintln(stderr, "prepare native runtime: session factory returned no session")
		return 1
	}
	defer application.Close()

	err = application.Run(ctx)
	return reportNativeRunResult(ctx, err, stderr)
}

func reportNativeRunResult(ctx context.Context, err error, stderr io.Writer) int {
	if err == nil || (ctx.Err() != nil && errors.Is(err, context.Canceled)) {
		return 0
	}
	if errors.Is(err, runtime.ErrRenewalRequired) {
		fmt.Fprintln(stderr, "renewal_required: run native-connect again to reauthenticate")
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
	resolveIP string,
	password []byte,
	readVerificationCode func() ([]byte, error),
) (*gatewayauth.Session, error) {
	client, err := gatewayauth.New(gatewayauth.Options{
		Server:        configured.Server,
		ResolveIP:     resolveIP,
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
		OnSOCKSListen: func(address string) {
			fmt.Fprintf(output, "socks: %s\n", address)
		},
		OnTraffic: func(snapshot nativeapp.TrafficSnapshot) {
			fmt.Fprintf(output, "traffic: upload=%d download=%d active=%d total=%d\n",
				snapshot.UploadBytes, snapshot.DownloadBytes, snapshot.ActiveConnections, snapshot.TotalConnections)
		},
		OnAccessEvidence: func(available bool) {
			fmt.Fprintf(output, "access: available=%t\n", available)
		},
	}
}
