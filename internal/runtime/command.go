package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

const (
	commandRequestSize = 64
	commandReplySize   = 36
	agentTokenSize     = sessiontoken.NativeGatewayTokenSize
)

type CommandDialer func(context.Context) (net.Conn, error)

type CommandIdentity struct {
	AssignedIPv4 netip.Addr
	HeartbeatLAN netip.Addr
}

type CommandConfig struct {
	Dial              CommandDialer
	Token             []byte
	HeartbeatInterval time.Duration
	InitialBackoff    time.Duration
	MaximumBackoff    time.Duration
	StableFor         time.Duration
	Wait              WaitFunc
	Now               func() time.Time
	OnIdentity        func(CommandIdentity) error
}

type CommandSupervisor struct {
	config CommandConfig
}

func NewCommandSupervisor(config CommandConfig) (*CommandSupervisor, error) {
	if config.Dial == nil {
		return nil, errors.New("command dialer is required")
	}
	if len(config.Token) != agentTokenSize {
		return nil, fmt.Errorf("agent token must be %d bytes", agentTokenSize)
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
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
	config.Token = append([]byte(nil), config.Token...)
	return &CommandSupervisor{config: config}, nil
}

func (supervisor *CommandSupervisor) Run(ctx context.Context, report func(Component, bool)) error {
	defer clear(supervisor.config.Token)
	backoff := newBoundedBackoff(supervisor.config.InitialBackoff, supervisor.config.MaximumBackoff)
	var identity CommandIdentity
	identityEstablished := false
	for {
		connection, current, err := supervisor.connect(ctx)
		if err != nil {
			if errors.Is(err, ErrGatewayRejected) {
				return &RenewalRequired{Reason: RenewalGatewayRejected}
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			report(ComponentCommand, false)
			if err := supervisor.config.Wait(ctx, backoff.Next()); err != nil {
				return err
			}
			continue
		}
		if identityEstablished && current != identity {
			_ = connection.Close()
			return &RenewalRequired{Reason: RenewalAddressChanged}
		}
		if !identityEstablished {
			identity = current
			identityEstablished = true
			if supervisor.config.OnIdentity != nil {
				if err := supervisor.config.OnIdentity(identity); err != nil {
					_ = connection.Close()
					return &TransportFailure{Code: FailureRuntimeStopped}
				}
			}
		}
		startedAt := supervisor.config.Now()
		report(ComponentCommand, true)
		err = supervisor.heartbeatUntilCanceled(ctx, connection)
		_ = connection.Close()
		report(ComponentCommand, false)
		if errors.Is(err, ErrGatewayRejected) {
			return &RenewalRequired{Reason: RenewalGatewayRejected}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if supervisor.config.Now().Sub(startedAt) >= supervisor.config.StableFor {
			backoff.Reset()
		}
		if err := supervisor.config.Wait(ctx, backoff.Next()); err != nil {
			return err
		}
	}
}

func (supervisor *CommandSupervisor) heartbeatUntilCanceled(ctx context.Context, connection net.Conn) error {
	stopped := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopped:
		}
	}()
	err := supervisor.heartbeat(ctx, connection)
	close(stopped)
	<-monitorDone
	return err
}

func (supervisor *CommandSupervisor) connect(ctx context.Context) (net.Conn, CommandIdentity, error) {
	connection, err := supervisor.config.Dial(ctx)
	if err != nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, CommandIdentity{}, err
	}
	if connection == nil {
		return nil, CommandIdentity{}, errors.New("command dialer returned no connection")
	}
	request := make([]byte, commandRequestSize)
	copy(request[4:52], supervisor.config.Token)
	binary.LittleEndian.PutUint32(request[60:64], 0xffffffff)
	err = writeFull(connection, request)
	clear(request)
	if err != nil {
		_ = connection.Close()
		return nil, CommandIdentity{}, err
	}
	reply := make([]byte, commandReplySize)
	defer clear(reply)
	if _, err := io.ReadFull(connection, reply); err != nil {
		_ = connection.Close()
		return nil, CommandIdentity{}, err
	}
	if binary.LittleEndian.Uint32(reply[0:4]) != 0 {
		_ = connection.Close()
		return nil, CommandIdentity{}, ErrGatewayRejected
	}
	identity := CommandIdentity{
		AssignedIPv4: netip.AddrFrom4([4]byte(reply[4:8])),
		HeartbeatLAN: netip.AddrFrom4([4]byte(reply[12:16])),
	}
	return connection, identity, nil
}

func (supervisor *CommandSupervisor) heartbeat(ctx context.Context, connection net.Conn) error {
	for {
		if err := supervisor.config.Wait(ctx, supervisor.config.HeartbeatInterval); err != nil {
			return err
		}
		request := make([]byte, commandRequestSize)
		binary.LittleEndian.PutUint32(request[0:4], 3)
		copy(request[4:52], supervisor.config.Token)
		err := writeFull(connection, request)
		clear(request)
		if err != nil {
			return err
		}
		reply := make([]byte, commandReplySize)
		if _, err := io.ReadFull(connection, reply); err != nil {
			clear(reply)
			return err
		}
		op := binary.LittleEndian.Uint32(reply[0:4])
		clear(reply)
		if op != 15 {
			return ErrGatewayRejected
		}
	}
}

func writeFull(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[written:]
	}
	return nil
}
