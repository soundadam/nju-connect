package atrustbackend

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/traffic"
)

// ConnectConfig describes one attended aTrust connection.
type ConnectConfig struct {
	Core  Core
	Login LoginRequest
	// SavedClientData, when non-empty, is tried with Core.Resume before any
	// interactive login. Connect does not retain or clear it.
	SavedClientData []byte
	Prompter        Prompter
	SOCKSListen     string
	// Direct and Resolver are optional; see RouterConfig.
	Direct   runtime.TCPDialer
	Resolver Resolver
	// Listen is optional and exists for tests; it defaults to net.Listen.
	Listen func(string, string) (net.Listener, error)
}

// Connection is a running aTrust session exposed through the shared SOCKS
// listener.
type Connection struct {
	session  Session
	tunnel   Tunnel
	router   *Router
	socks    *runtime.SOCKSServer
	counters *traffic.Counters
	resumed  bool

	closeOnce sync.Once
	closeErr  error
}

// Connect resumes or authenticates a session, loads its resources, opens the
// tunnel, and binds the SOCKS listener. It does not start serving; call Run.
func Connect(ctx context.Context, config ConnectConfig) (*Connection, error) {
	if config.Core == nil {
		return nil, errors.New("aTrust protocol core is required")
	}
	if err := validateEndpoint(config.Login.Endpoint); err != nil {
		return nil, err
	}
	if config.SOCKSListen == "" {
		return nil, errors.New("SOCKS listener is required")
	}

	session, resumed, err := establishSession(ctx, config)
	if err != nil {
		return nil, err
	}
	connection := &Connection{session: session, resumed: resumed, counters: &traffic.Counters{}}
	if err := connection.prepare(ctx, config); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func establishSession(ctx context.Context, config ConnectConfig) (Session, bool, error) {
	if len(config.SavedClientData) > 0 {
		session, err := config.Core.Resume(ctx, ResumeRequest{
			Endpoint:   config.Login.Endpoint,
			ClientData: config.SavedClientData,
			Dial:       config.Login.Dial,
		})
		if err == nil {
			return session, true, nil
		}
		if !errors.Is(err, ErrSessionExpired) {
			return nil, false, err
		}
	}
	if config.Prompter == nil {
		return nil, false, errors.New("aTrust login requires an interactive prompter")
	}
	session, err := config.Core.Authenticate(ctx, config.Login, config.Prompter)
	if err != nil {
		return nil, false, err
	}
	return session, false, nil
}

func (connection *Connection) prepare(ctx context.Context, config ConnectConfig) error {
	resources, err := connection.session.Resources(ctx)
	if err != nil {
		return err
	}
	tunnel, err := connection.session.OpenTunnel(ctx)
	if err != nil {
		return err
	}
	connection.tunnel = tunnel
	router, err := NewRouter(RouterConfig{
		Resources: resources,
		Tunnel:    tunnel,
		Direct:    config.Direct,
		Resolver:  config.Resolver,
	})
	if err != nil {
		return err
	}
	connection.router = router
	socks, err := runtime.NewSOCKSServer(runtime.SOCKSConfig{
		Bind:        config.SOCKSListen,
		Dialer:      router,
		ResolveIPv4: router.ResolveIPv4,
		Counters:    connection.counters,
		Listen:      config.Listen,
	})
	if err != nil {
		return err
	}
	connection.socks = socks
	return nil
}

// Resumed reports whether saved client data restored the session.
func (connection *Connection) Resumed() bool { return connection.resumed }

// ClientData returns fresh opaque session data to persist. The caller clears
// the returned slice.
func (connection *Connection) ClientData() ([]byte, error) {
	return connection.session.ClientData()
}

// SOCKSAddr is the bound listener address.
func (connection *Connection) SOCKSAddr() net.Addr { return connection.socks.Addr() }

// Traffic returns payload counters measured at the SOCKS ingress.
func (connection *Connection) Traffic() traffic.Snapshot { return connection.counters.Snapshot() }

// Run serves SOCKS and maintains the tunnel until ctx ends or either fails.
// It returns nil when ctx ends normally.
func (connection *Connection) Run(ctx context.Context) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	connection.counters.BeginSession(time.Now())

	results := make(chan error, 2)
	go func() { results <- connection.tunnel.Run(runContext) }()
	go func() { results <- connection.socks.Run(runContext, func(runtime.Component, bool) {}) }()

	first := <-results
	cancel()
	<-results
	if ctx.Err() != nil {
		return nil
	}
	if first == nil || errors.Is(first, context.Canceled) {
		return errors.New("aTrust tunnel stopped unexpectedly")
	}
	return first
}

// Close releases the tunnel and session. It is idempotent.
func (connection *Connection) Close() error {
	connection.closeOnce.Do(func() {
		var errs []error
		if connection.socks != nil {
			if err := connection.socks.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				errs = append(errs, err)
			}
		}
		if connection.tunnel != nil {
			errs = append(errs, connection.tunnel.Close())
		}
		if connection.session != nil {
			errs = append(errs, connection.session.Close())
		}
		connection.closeErr = errors.Join(errs...)
	})
	return connection.closeErr
}
