package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/backend/easyconnect/auth"
	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/core"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/dial"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
	"github.com/soundadam/nju-connect/internal/sessiontoken"
)

const (
	upstreamPreflightTimeout = 3 * time.Second
	gatewayAuthTimeout       = 30 * time.Second
	// maximumCredentialAttempts bounds sign-in attempts in one connect,
	// counting the first, when a person re-enters a rejected password.
	maximumCredentialAttempts = 3
)

// NativeSession is the in-process EasyConnect runtime.
type NativeSession interface {
	Run(context.Context) error
	Close() error
	Traffic() nativeapp.TrafficSnapshot
	Profile() runtime.ProtocolProfileMetadata
}

// NewEasyConnectSession is the production Deps.EasyConnectSession.
func NewEasyConnectSession(sessionConfig nativeapp.SessionConfig) (NativeSession, error) {
	return nativeapp.NewSession(sessionConfig)
}

// ConnectEvents receives a connection's progress. Nil fields are skipped.
type ConnectEvents struct {
	// Authenticated reports a successful sign-in. resumed is true when a
	// saved aTrust session was accepted without a new login.
	Authenticated func(name backend.Name, resumed bool)
	// Started reports the EasyConnect runtime's protocol profile.
	Started func(runtime.ProtocolProfileMetadata)
	// Runtime receives runtime state, listener, and traffic events.
	Runtime nativeapp.ObserverFuncs
}

func (events ConnectEvents) authenticated(name backend.Name, resumed bool) {
	if events.Authenticated != nil {
		events.Authenticated(name, resumed)
	}
}

func (events ConnectEvents) started(profile runtime.ProtocolProfileMetadata) {
	if events.Started != nil {
		events.Started(profile)
	}
}

// ConnectRequest selects how Connect runs.
type ConnectRequest struct {
	// Background hands the session to a detached runtime and returns once it
	// is connected. Only EasyConnect supports it.
	Background bool
	// OfferSetup lets a person without a configuration run the guided setup
	// first. It is false when answers are piped in.
	OfferSetup bool
}

// ConnectResult describes a background runtime Connect started.
type ConnectResult struct {
	BackgroundPID int
	LogPath       string
}

// CredentialRejectedError reports a rejected username or password that
// nobody was at the terminal to correct. Its message starts with the stable
// token "credential_rejected:", which the macOS app matches.
type CredentialRejectedError struct {
	cause error
}

func (rejected *CredentialRejectedError) Error() string {
	return fmt.Sprintf(`credential_rejected: %v; run "nju-connect account set-password"`, rejected.cause)
}

func (rejected *CredentialRejectedError) Unwrap() error { return rejected.cause }

// errNoPassword tells the user how to save the missing password.
var errNoPassword = errors.New(`no saved VPN password; run "nju-connect account set-password"`)

// Connect signs in with the saved profile and runs the VPN until ctx ends or
// `disconnect` stops it. When the gateway rejects the saved username or
// password, a person at the terminal may correct them and try again;
// otherwise Connect returns a CredentialRejectedError.
func Connect(ctx context.Context, deps Deps, request ConnectRequest, events ConnectEvents) (ConnectResult, error) {
	paths, err := deps.Paths()
	if err != nil {
		return ConnectResult{}, fmt.Errorf("resolve local state: %w", err)
	}
	if err := runtimecontrol.EnsureNoActive(runtimecontrol.Path(paths.Root)); err != nil {
		if errors.Is(err, runtimecontrol.ErrAlreadyActive) {
			return ConnectResult{}, errors.New(`nju-connect is already running; run "nju-connect status" to inspect it or "nju-connect disconnect" to stop it`)
		}
		return ConnectResult{}, fmt.Errorf("prepare runtime status: %w", err)
	}
	configured, err := LoadProfile(ctx, deps, request.OfferSetup)
	if err != nil {
		return ConnectResult{}, err
	}

	preflightContext, cancelPreflight := context.WithTimeout(ctx, upstreamPreflightTimeout)
	err = dial.ProbeUpstream(preflightContext, configured.UpstreamProxy)
	cancelPreflight()
	if err != nil {
		return ConnectResult{}, fmt.Errorf("upstream preflight: %w", err)
	}
	if configured.BackendName() == backend.ATrust {
		if request.Background {
			return ConnectResult{}, Usagef(`--background is EasyConnect only; run "nju-connect connect" in the foreground for aTrust`)
		}
		return ConnectResult{}, connectATrust(ctx, deps, paths, configured, events)
	}
	return connectEasyConnect(ctx, deps, paths, configured, request.Background, events)
}

// LoadProfile loads the configuration for a command that needs one. On
// first run a person at the terminal is offered the guided setup when
// offerSetup is true; everyone else is told to run setup.
func LoadProfile(ctx context.Context, deps Deps, offerSetup bool) (config.Config, error) {
	paths, err := deps.Paths()
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve local state: %w", err)
	}
	configured, err := config.Load(paths.Config)
	if err == nil {
		return configured, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, fmt.Errorf("load configuration: %w", err)
	}
	if offerSetup && deps.Interactive {
		runSetup, err := deps.Interaction.Confirm(ctx, "No configuration yet. Run setup now?", true)
		if err != nil {
			return config.Config{}, err
		}
		if runSetup {
			result, err := Setup(ctx, deps, SetupRequest{Guided: true})
			if err != nil {
				return config.Config{}, err
			}
			return result.Config, nil
		}
	}
	return config.Config{}, errors.New(`load configuration: no configuration yet; run "nju-connect setup" first`)
}

// readSavedPassword reads the shared password, explaining how to save one
// when there is none.
func readSavedPassword(deps Deps, paths config.Paths, configured config.Config) ([]byte, error) {
	store, err := deps.PasswordStore(paths.Credential)
	if err != nil {
		return nil, fmt.Errorf("open credential: %w", err)
	}
	password, err := store.Get()
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, credential.ErrEmptyCredential) {
		return nil, errNoPassword
	}
	if err != nil {
		return nil, fmt.Errorf("read credential: %w", err)
	}
	return password, nil
}

// signIn runs attempt until it succeeds or fails for a reason other than a
// rejected credential. After a rejection a person at the terminal may
// correct the saved account; configured is updated to match.
func signIn(ctx context.Context, deps Deps, configured *config.Config, attempt func() error) error {
	for attempts := 1; ; attempts++ {
		err := attempt()
		if !errors.Is(err, backend.ErrCredentialRejected) {
			return err
		}
		if !deps.Interactive || attempts >= maximumCredentialAttempts {
			return &CredentialRejectedError{cause: err}
		}
		fmt.Fprintln(deps.diagnostics(), err)
		if err := correctCredential(ctx, deps, configured); err != nil {
			return err
		}
	}
}

// correctCredential asks a person to re-enter the rejected password, or to
// change the username too, and saves the answer.
func correctCredential(ctx context.Context, deps Deps, configured *config.Config) error {
	choice, err := deps.Interaction.Select(ctx, "The gateway rejected the saved username or password", []Option{
		{Value: "password", Label: "Enter the password again"},
		{Value: "account", Label: "Change the username and password"},
		{Value: "stop", Label: "Stop"},
	}, "password")
	if err != nil {
		return err
	}
	switch choice {
	case "stop":
		return context.Canceled
	case "account":
		username, err := deps.Interaction.Input(ctx, "Username", configured.Username, requireValue("username"))
		if err != nil {
			return err
		}
		if _, err := SetUsername(deps, username); err != nil {
			return err
		}
		configured.Username = username
	}
	return SetPassword(ctx, deps)
}

func requireValue(name string) func(string) error {
	return func(value string) error {
		if value == "" {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
}

func connectEasyConnect(
	ctx context.Context,
	deps Deps,
	paths config.Paths,
	configured config.Config,
	background bool,
	events ConnectEvents,
) (ConnectResult, error) {
	if background && deps.StartBackground == nil {
		return ConnectResult{}, errors.New("prepare native runtime: background starter is unavailable")
	}
	if !background && deps.EasyConnectSession == nil {
		return ConnectResult{}, errors.New("prepare native runtime: session factory is unavailable")
	}
	var session *gatewayauth.Session
	err := signIn(ctx, deps, &configured, func() error {
		password, err := readSavedPassword(deps, paths, configured)
		if err != nil {
			return err
		}
		defer credential.Clear(password)
		session, err = authenticateEasyConnect(ctx, configured, password, func() ([]byte, error) {
			return deps.Interaction.VerificationCode(ctx, "")
		})
		if err != nil && !errors.Is(err, backend.ErrCredentialRejected) {
			return fmt.Errorf("authenticate gateway: %w", err)
		}
		return err
	})
	if err != nil {
		return ConnectResult{}, err
	}
	defer session.Close()
	events.authenticated(backend.EasyConnect, false)

	bootstrap, err := session.ProbeBootstrap(ctx)
	if err != nil {
		return ConnectResult{}, fmt.Errorf("initialize gateway session: %w", err)
	}
	plan, err := core.BuildDataplanePlan(session.State(), bootstrap)
	if err != nil {
		return ConnectResult{}, fmt.Errorf("model native dataplane: %w", err)
	}

	logPath := filepath.Join(paths.Root, "runtime.log")
	var application NativeSession
	var backgroundPID int
	statusTracker := NewRuntimeStatusTracker(runtime.ProfileCommunityUTLSCompat)
	err = session.WithNativeGatewayToken(func(token sessiontoken.NativeGatewayToken) error {
		sessionConfig := nativeapp.SessionConfig{
			Settings:               configured,
			Plan:                   plan,
			NativeGatewayToken:     token,
			NativeProfile:          runtime.ProfileCommunityUTLSCompat,
			TrafficPublishInterval: time.Second,
		}
		if background {
			var startErr error
			backgroundPID, startErr = deps.StartBackground(sessionConfig, logPath)
			return startErr
		}
		var buildErr error
		sessionConfig.Observer = RuntimeStatusObserver(events.Runtime, statusTracker)
		application, buildErr = deps.EasyConnectSession(sessionConfig)
		return buildErr
	})
	if err != nil {
		return ConnectResult{}, fmt.Errorf("prepare native runtime: %w", err)
	}
	if background {
		return ConnectResult{BackgroundPID: backgroundPID, LogPath: logPath}, nil
	}
	if application == nil {
		return ConnectResult{}, errors.New("prepare native runtime: session factory returned no session")
	}
	defer application.Close()
	statusServer, err := runtimecontrol.Serve(runtimecontrol.Path(paths.Root), func() runtimecontrol.Snapshot {
		statusTracker.UpdateTraffic(application.Traffic(), time.Now())
		return statusTracker.Snapshot()
	})
	if err != nil {
		return ConnectResult{}, fmt.Errorf("prepare runtime status: %w", err)
	}
	defer statusServer.Close()
	events.started(application.Profile())

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	statusServer.SetStop(cancelRuntime)
	return ConnectResult{}, runResult(runtimeContext, application.Run(runtimeContext))
}

// runResult turns how an EasyConnect runtime ended into the error the user
// sees: nil when it was stopped on purpose.
func runResult(ctx context.Context, err error) error {
	if err == nil || (ctx.Err() != nil && errors.Is(err, context.Canceled)) {
		return nil
	}
	if errors.Is(err, runtime.ErrRenewalRequired) {
		return errors.New(`renewal_required: run "nju-connect connect" to sign in again`)
	}
	var failure *runtime.TransportFailure
	if errors.As(err, &failure) {
		return fmt.Errorf("native transport: %s", failure.Error())
	}
	return errors.New("native transport: runtime_stopped")
}

// authenticateEasyConnect signs in to an EasyConnect gateway, asking for a
// verification code when the gateway wants one. A refused password wraps
// backend.ErrCredentialRejected.
func authenticateEasyConnect(
	ctx context.Context,
	configured config.Config,
	password []byte,
	readVerificationCode func() ([]byte, error),
) (*gatewayauth.Session, error) {
	client, err := gatewayauth.New(gatewayauth.Options{
		Server:        configured.Server,
		TLSInsecure:   configured.TLSInsecure,
		UpstreamProxy: configured.UpstreamProxy,
		Timeout:       gatewayAuthTimeout,
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
	} else if !result.Accepted() && result.NextService == "" {
		return nil, fmt.Errorf("%w (gateway code %d)", backend.ErrCredentialRejected, result.Code)
	}
	if !result.Accepted() {
		if result.NextService != "" {
			return nil, errors.New("gateway requires an unsupported authentication step")
		}
		return nil, fmt.Errorf("gateway rejected authentication with code %d", result.Code)
	}
	return client.TakeSession()
}
