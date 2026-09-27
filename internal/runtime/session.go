package runtime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/soundadam/nju-connect/internal/core"
	"github.com/soundadam/nju-connect/internal/sessiontoken"
	"github.com/soundadam/nju-connect/internal/traffic"
)

// NativeDataStreamOpenFunc opens an authenticated gateway protocol stream. The
// token is a temporary copy valid only for the duration of the call;
// implementations must not retain it. The returned byte is the gateway
// handshake reply.
type NativeDataStreamOpenFunc func(context.Context, StreamKind, sessiontoken.NativeGatewayToken, netip.Addr) (io.ReadWriteCloser, byte, error)

type NativeSessionConfig struct {
	Plan           core.DataplanePlan
	AgentToken     sessiontoken.NativeGatewayToken
	Profile        ProtocolProfile
	OpenDataStream NativeDataStreamOpenFunc

	SOCKSBind        string
	ResolveIPv4      ResolveIPv4Func
	MaxSOCKSClients  int
	Counters         *traffic.Counters
	AccessURL        string
	OnAccessEvidence AccessEvidenceFunc

	OnState          func(State)
	OnListen         func(net.Addr)
	Now              func() time.Time
	OnCommandFailure func(CommandFailure)
	OnDataFailure    func(FailureStage)

	Watchdog                   time.Duration
	CommandHeartbeat           time.Duration
	CommandAttemptTimeout      time.Duration
	CommandInitialAttemptLimit int
	DataHeartbeat              time.Duration
	ReconnectInitialBackoff    time.Duration
	ReconnectMaximumBackoff    time.Duration
	StableFor                  time.Duration
	MaxPendingIPv4             int
}

type nativeResources struct {
	userspace *Userspace
	cohort    *CohortSupervisor
	socks     *SOCKSSupervisor
}

type NativeSession struct {
	config NativeSessionConfig
	token  sessiontoken.NativeGatewayToken

	initialized chan struct{}
	initOnce    sync.Once
	resourcesMu sync.RWMutex
	resources   nativeResources

	listenMu sync.RWMutex
	listen   string

	lifecycleMu sync.Mutex
	started     bool
	closed      bool
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewNativeSession(config NativeSessionConfig) (*NativeSession, error) {
	if err := validateNativePlan(config.Plan); err != nil {
		return nil, err
	}
	if len(config.AgentToken) != agentTokenSize {
		return nil, errors.New("native session requires a 48-byte agent token")
	}
	if config.Profile == nil {
		return nil, errors.New("native protocol profile is required")
	}
	if config.OpenDataStream == nil {
		opener, err := NewAuthenticatedDataStreamOpener(config.Profile, config.OnDataFailure)
		if err != nil {
			return nil, err
		}
		config.OpenDataStream = opener.Open
	}
	if err := validateLoopbackBind(config.SOCKSBind); err != nil {
		return nil, err
	}
	if config.AccessURL != "" {
		if _, err := NewHEADProbe(&http.Client{}, config.AccessURL); err != nil {
			return nil, err
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Counters == nil {
		config.Counters = &traffic.Counters{}
	}
	token := append(sessiontoken.NativeGatewayToken(nil), config.AgentToken...)
	config.AgentToken = nil
	return &NativeSession{
		config:      config,
		token:       token,
		initialized: make(chan struct{}),
	}, nil
}

func validateNativePlan(plan core.DataplanePlan) error {
	if !plan.BoundaryReady || !plan.LocalAgentRequired {
		return errors.New("native runtime requires a ready authenticated dataplane boundary")
	}
	if plan.Mode != core.DataplaneL3VPN && plan.Mode != core.DataplaneHybrid {
		return errors.New("native runtime requires an L3VPN dataplane plan")
	}
	return nil
}

func (session *NativeSession) Traffic() traffic.Snapshot {
	return session.config.Counters.Snapshot()
}

func (session *NativeSession) Run(ctx context.Context) error {
	session.lifecycleMu.Lock()
	if session.started || session.closed {
		session.lifecycleMu.Unlock()
		return errors.New("native session cannot be run more than once")
	}
	session.started = true
	sessionContext, cancel := context.WithCancel(ctx)
	session.cancel = cancel
	session.done = make(chan struct{})
	done := session.done
	session.lifecycleMu.Unlock()

	defer func() {
		cancel()
		session.closeResources()
		clear(session.token)
		session.token = nil
		session.lifecycleMu.Lock()
		session.closed = true
		close(done)
		session.lifecycleMu.Unlock()
	}()
	session.config.Counters.BeginSession(session.config.Now())

	command, err := NewCommandSupervisor(CommandConfig{
		Profile:             session.config.Profile,
		Token:               session.token,
		HeartbeatInterval:   session.config.CommandHeartbeat,
		InitialBackoff:      session.config.ReconnectInitialBackoff,
		MaximumBackoff:      session.config.ReconnectMaximumBackoff,
		StableFor:           session.config.StableFor,
		AttemptTimeout:      session.config.CommandAttemptTimeout,
		InitialAttemptLimit: session.config.CommandInitialAttemptLimit,
		Now:                 session.config.Now,
		OnIdentity:          session.initialize,
		OnFailure:           session.config.OnCommandFailure,
	})
	if err != nil {
		return &TransportFailure{Code: FailureRuntimeStopped}
	}

	stateEvents := make(chan State, componentCount*2)
	var probeDone <-chan error
	if session.config.AccessURL != "" {
		coordinator, err := NewAccessProbeCoordinator(AccessProbeConfig{
			Probe:    session.accessProbe,
			Evidence: session.config.OnAccessEvidence,
		})
		if err != nil {
			return &TransportFailure{Code: FailureRuntimeStopped}
		}
		result := make(chan error, 1)
		probeDone = result
		go func() { result <- coordinator.Run(sessionContext, stateEvents) }()
	}

	owner, err := NewOwner(OwnerConfig{
		Runners: []Runner{
			command,
			RunnerFunc(session.runCohort),
			RunnerFunc(session.runSOCKS),
		},
		Watchdog: session.config.Watchdog,
		Now:      session.config.Now,
		OnState: func(state State) {
			if session.config.OnState != nil {
				session.config.OnState(state)
			}
			if probeDone != nil {
				select {
				case stateEvents <- state:
				case <-sessionContext.Done():
				}
			}
		},
	})
	if err != nil {
		cancel()
		if probeDone != nil {
			<-probeDone
		}
		return &TransportFailure{Code: FailureRuntimeStopped}
	}
	err = owner.Run(sessionContext)
	cancel()
	if probeDone != nil {
		<-probeDone
	}
	return err
}

func (session *NativeSession) initialize(identity CommandIdentity) error {
	var initializeErr error
	session.initOnce.Do(func() {
		userspace, err := NewUserspace(identity.AssignedIPv4)
		if err != nil {
			initializeErr = err
			return
		}
		open := func(ctx context.Context, kind StreamKind) (io.ReadWriteCloser, byte, error) {
			token := append(sessiontoken.NativeGatewayToken(nil), session.token...)
			defer clear(token)
			return session.config.OpenDataStream(ctx, kind, token, identity.AssignedIPv4)
		}
		heartbeat := func() ([]byte, error) {
			return BuildICMPHeartbeat(identity.AssignedIPv4, identity.HeartbeatLAN, session.token)
		}
		factory, err := NewDataStreamFactory(open, userspace, identity, heartbeat, session.config.MaxPendingIPv4, session.config.DataHeartbeat)
		if err != nil {
			_ = userspace.Close()
			initializeErr = err
			return
		}
		cohort, err := NewCohortSupervisor(CohortConfig{
			Factory:        factory,
			InitialBackoff: session.config.ReconnectInitialBackoff,
			MaximumBackoff: session.config.ReconnectMaximumBackoff,
			StableFor:      session.config.StableFor,
			Now:            session.config.Now,
			OnFailure:      session.config.OnDataFailure,
		})
		if err != nil {
			_ = userspace.Close()
			initializeErr = err
			return
		}
		socks, err := NewSOCKSSupervisor(SOCKSSupervisorConfig{
			Server: SOCKSConfig{
				Bind:           session.config.SOCKSBind,
				Dialer:         userspace,
				ResolveIPv4:    session.config.ResolveIPv4,
				Counters:       session.config.Counters,
				MaxConnections: session.config.MaxSOCKSClients,
			},
			InitialBackoff: session.config.ReconnectInitialBackoff,
			MaximumBackoff: session.config.ReconnectMaximumBackoff,
			StableFor:      session.config.StableFor,
			Now:            session.config.Now,
			OnListen:       session.setListenAddress,
		})
		if err != nil {
			_ = userspace.Close()
			initializeErr = err
			return
		}
		session.resourcesMu.Lock()
		session.resources = nativeResources{userspace: userspace, cohort: cohort, socks: socks}
		session.resourcesMu.Unlock()
		close(session.initialized)
	})
	return initializeErr
}

func (session *NativeSession) runCohort(ctx context.Context, report func(Component, bool)) error {
	resources, err := session.waitResources(ctx)
	if err != nil {
		return err
	}
	return resources.cohort.Run(ctx, report)
}

func (session *NativeSession) runSOCKS(ctx context.Context, report func(Component, bool)) error {
	resources, err := session.waitResources(ctx)
	if err != nil {
		return err
	}
	return resources.socks.Run(ctx, report)
}

func (session *NativeSession) waitResources(ctx context.Context) (nativeResources, error) {
	select {
	case <-ctx.Done():
		return nativeResources{}, ctx.Err()
	case <-session.initialized:
		session.resourcesMu.RLock()
		defer session.resourcesMu.RUnlock()
		return session.resources, nil
	}
}

func (session *NativeSession) setListenAddress(address net.Addr) {
	session.listenMu.Lock()
	session.listen = address.String()
	session.listenMu.Unlock()
	if session.config.OnListen != nil {
		session.config.OnListen(address)
	}
}

func (session *NativeSession) accessProbe(ctx context.Context) error {
	session.listenMu.RLock()
	address := session.listen
	session.listenMu.RUnlock()
	if address == "" {
		return errors.New("SOCKS listener is not ready")
	}
	client, err := NewSOCKSHTTPClient(address)
	if err != nil {
		return errors.New("build access probe client")
	}
	if transport, ok := client.Transport.(*http.Transport); ok {
		defer transport.CloseIdleConnections()
	}
	probe, err := NewHEADProbe(client, session.config.AccessURL)
	if err != nil {
		return err
	}
	return probe(ctx)
}

func (session *NativeSession) closeResources() {
	session.resourcesMu.RLock()
	userspace := session.resources.userspace
	session.resourcesMu.RUnlock()
	if userspace != nil {
		_ = userspace.Close()
	}
}

// Close cancels and joins a running session. A replacement authentication
// generation must call Close before constructing and running its successor.
func (session *NativeSession) Close() error {
	if session == nil {
		return nil
	}
	session.lifecycleMu.Lock()
	if !session.started {
		session.closed = true
		clear(session.token)
		session.token = nil
		session.lifecycleMu.Unlock()
		return nil
	}
	cancel := session.cancel
	done := session.done
	session.lifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}
