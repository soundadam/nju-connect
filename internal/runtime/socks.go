package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/soundadam/soundconnect/internal/traffic"
	"golang.org/x/net/proxy"
)

const maxSOCKSConnections = 256

type ResolveIPv4Func func(context.Context, string) (netip.Addr, error)

type SOCKSConfig struct {
	Bind           string
	Dialer         TCPDialer
	ResolveIPv4    ResolveIPv4Func
	Counters       *traffic.Counters
	MaxConnections int
	Listen         func(string, string) (net.Listener, error)
}

type SOCKSServer struct {
	listener net.Listener
	config   SOCKSConfig
	limit    chan struct{}
}

type SOCKSSupervisorConfig struct {
	Server         SOCKSConfig
	InitialBackoff time.Duration
	MaximumBackoff time.Duration
	StableFor      time.Duration
	Wait           WaitFunc
	Now            func() time.Time
	OnListen       func(net.Addr)
}

type SOCKSSupervisor struct {
	config SOCKSSupervisorConfig
}

func NewSOCKSSupervisor(config SOCKSSupervisorConfig) (*SOCKSSupervisor, error) {
	if err := validateSOCKSConfig(config.Server); err != nil {
		return nil, err
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = 4 * time.Second
	}
	if config.MaximumBackoff <= 0 {
		config.MaximumBackoff = 30 * time.Second
	}
	if config.StableFor <= 0 {
		config.StableFor = time.Minute
	}
	if config.Wait == nil {
		config.Wait = waitContext
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &SOCKSSupervisor{config: config}, nil
}

func (supervisor *SOCKSSupervisor) Run(ctx context.Context, report func(Component, bool)) error {
	backoff := newBoundedBackoff(supervisor.config.InitialBackoff, supervisor.config.MaximumBackoff)
	for {
		startedAt := supervisor.config.Now()
		server, err := NewSOCKSServer(supervisor.config.Server)
		if err == nil {
			if supervisor.config.OnListen != nil {
				supervisor.config.OnListen(server.Addr())
			}
			err = server.Run(ctx, report)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if supervisor.config.Now().Sub(startedAt) >= supervisor.config.StableFor {
			backoff.Reset()
		}
		report(ComponentSOCKS, false)
		if err := supervisor.config.Wait(ctx, backoff.Next()); err != nil {
			return err
		}
	}
}

func NewSOCKSServer(config SOCKSConfig) (*SOCKSServer, error) {
	if err := validateSOCKSConfig(config); err != nil {
		return nil, err
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = maxSOCKSConnections
	}
	if config.MaxConnections < 1 || config.MaxConnections > maxSOCKSConnections {
		return nil, errors.New("SOCKS connection limit must be between 1 and 256")
	}
	if config.Listen == nil {
		config.Listen = net.Listen
	}
	listener, err := config.Listen("tcp", config.Bind)
	if err != nil {
		return nil, &TransportFailure{Code: FailureTransportUnavailable}
	}
	return &SOCKSServer{listener: listener, config: config, limit: make(chan struct{}, config.MaxConnections)}, nil
}

func validateSOCKSConfig(config SOCKSConfig) error {
	if config.Dialer == nil {
		return errors.New("SOCKS userspace TCP dialer is required")
	}
	if err := validateLoopbackBind(config.Bind); err != nil {
		return err
	}
	if config.MaxConnections < 0 || config.MaxConnections > maxSOCKSConnections {
		return errors.New("SOCKS connection limit must be between 1 and 256")
	}
	return nil
}

func (server *SOCKSServer) Addr() net.Addr { return server.listener.Addr() }

func (server *SOCKSServer) Run(ctx context.Context, report func(Component, bool)) error {
	serveContext, cancel := context.WithCancel(ctx)
	defer cancel()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		<-serveContext.Done()
		_ = server.listener.Close()
	}()
	report(ComponentSOCKS, true)
	defer report(ComponentSOCKS, false)
	var connections sync.WaitGroup
	defer func() {
		cancel()
		_ = server.listener.Close()
		connections.Wait()
		<-closed
	}()
	for {
		select {
		case server.limit <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		connection, err := server.listener.Accept()
		if err != nil {
			<-server.limit
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &TransportFailure{Code: FailureTransportUnavailable}
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			defer func() { <-server.limit }()
			server.handle(serveContext, connection)
		}()
	}
}

func (server *SOCKSServer) handle(ctx context.Context, client net.Conn) {
	defer client.Close()
	connectionContext, cancel := context.WithCancel(ctx)
	monitorDone := closeOnCancel(connectionContext, client)
	defer func() {
		cancel()
		<-monitorDone
	}()
	if err := negotiateSOCKS(client); err != nil {
		return
	}
	destination, reply, err := server.readDestination(connectionContext, client)
	if err != nil {
		_ = writeSOCKSReply(client, reply)
		return
	}
	upstream, err := server.config.Dialer.DialContext(connectionContext, "tcp4", destination.String())
	if err != nil {
		_ = writeSOCKSReply(client, 0x05)
		return
	}
	defer upstream.Close()
	upstreamMonitor := closeOnCancel(connectionContext, upstream)
	defer func() {
		cancel()
		<-upstreamMonitor
	}()
	if err := writeSOCKSReply(client, 0x00); err != nil {
		return
	}
	server.config.Counters.ConnectionOpened()
	defer server.config.Counters.ConnectionClosed()
	relayPayload(client, upstream, server.config.Counters)
}

func negotiateSOCKS(connection net.Conn) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 || header[1] == 0 {
		return errors.New("invalid SOCKS greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(connection, methods); err != nil {
		return err
	}
	accepted := false
	for _, method := range methods {
		accepted = accepted || method == 0
	}
	if !accepted {
		_, _ = connection.Write([]byte{5, 0xff})
		return errors.New("SOCKS no-auth method was not offered")
	}
	return writeFull(connection, []byte{5, 0})
}

func (server *SOCKSServer) readDestination(ctx context.Context, connection net.Conn) (netip.AddrPort, byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(connection, header); err != nil {
		return netip.AddrPort{}, 0x01, err
	}
	if header[0] != 5 || header[1] != 1 || header[2] != 0 {
		return netip.AddrPort{}, 0x07, errors.New("SOCKS request is not TCP CONNECT")
	}
	var address netip.Addr
	switch header[3] {
	case 1:
		value := [4]byte{}
		if _, err := io.ReadFull(connection, value[:]); err != nil {
			return netip.AddrPort{}, 0x01, err
		}
		address = netip.AddrFrom4(value)
	case 3:
		length := []byte{0}
		if _, err := io.ReadFull(connection, length); err != nil || length[0] == 0 {
			return netip.AddrPort{}, 0x04, errors.New("invalid SOCKS domain")
		}
		domain := make([]byte, int(length[0]))
		if _, err := io.ReadFull(connection, domain); err != nil {
			return netip.AddrPort{}, 0x04, err
		}
		if server.config.ResolveIPv4 == nil {
			return netip.AddrPort{}, 0x04, errors.New("SOCKS domain resolver is unavailable")
		}
		resolved, err := server.config.ResolveIPv4(ctx, string(domain))
		clear(domain)
		if err != nil || !resolved.Is4() {
			return netip.AddrPort{}, 0x04, errors.New("SOCKS domain resolution failed")
		}
		address = resolved
	default:
		return netip.AddrPort{}, 0x08, errors.New("SOCKS address type is unsupported")
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(connection, port); err != nil {
		return netip.AddrPort{}, 0x01, err
	}
	return netip.AddrPortFrom(address, binary.BigEndian.Uint16(port)), 0, nil
}

func writeSOCKSReply(connection net.Conn, status byte) error {
	return writeFull(connection, []byte{5, status, 0, 1, 0, 0, 0, 0, 0, 0})
}

func relayPayload(client, upstream net.Conn, counters *traffic.Counters) {
	completed := make(chan struct{}, 2)
	copyDirection := func(destination, source net.Conn, account func(uint64)) {
		written, _ := io.Copy(destination, source)
		if written > 0 {
			account(uint64(written))
		}
		if closeWriter, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = closeWriter.CloseWrite()
		}
		completed <- struct{}{}
	}
	go copyDirection(upstream, client, counters.AddUpload)
	go copyDirection(client, upstream, counters.AddDownload)
	<-completed
	<-completed
}

func validateLoopbackBind(bind string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(bind))
	if err != nil || port == "" {
		return errors.New("SOCKS bind must be loopback host:port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("SOCKS bind port is invalid")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.IsLoopback() {
		return errors.New("SOCKS bind must use numeric loopback or localhost")
	}
	return nil
}

func NewSOCKSHTTPClient(address string) (*http.Client, error) {
	if err := validateLoopbackBind(address); err != nil {
		return nil, err
	}
	base := &net.Dialer{}
	dialer, err := proxy.SOCKS5("tcp", address, nil, base)
	if err != nil {
		return nil, errors.New("create access-probe SOCKS dialer")
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("access-probe SOCKS dialer lacks cancellation")
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:       nil,
		DialContext: contextDialer.DialContext,
	}}, nil
}
