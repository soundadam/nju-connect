package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"
)

const gatewayProtocolTimeout = 15 * time.Second

type AuthenticatedDataStreamOpener struct {
	dial CommandDialer
	now  func() time.Time
}

func NewAuthenticatedDataStreamOpener(dial CommandDialer) (*AuthenticatedDataStreamOpener, error) {
	if dial == nil {
		return nil, errors.New("authenticated data stream dialer is required")
	}
	return &AuthenticatedDataStreamOpener{dial: dial, now: time.Now}, nil
}

func (opener *AuthenticatedDataStreamOpener) Open(ctx context.Context, kind StreamKind, token []byte, assigned netip.Addr) (io.ReadWriteCloser, byte, error) {
	if _, err := ExpectedStreamReply(kind); err != nil {
		return nil, 0, err
	}
	if len(token) != agentTokenSize || !assigned.Is4() {
		return nil, 0, &TransportFailure{Code: FailureProtocolInvalid}
	}
	connection, err := opener.dial(ctx)
	if err != nil {
		if connection != nil {
			_ = connection.Close()
		}
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if connection == nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = connection.Close()
		}
	}()
	stopMonitor := monitorContext(ctx, connection)
	defer stopMonitor()
	if err := connection.SetDeadline(protocolDeadline(ctx, opener.now())); err != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}

	request := make([]byte, commandRequestSize)
	request[0] = byte(kind)
	copy(request[4:52], token)
	address := assigned.As4()
	for index := range address {
		request[60+index] = address[len(address)-1-index]
	}
	if err := writeFull(connection, request); err != nil {
		clear(request)
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	clear(request)
	reply := []byte{0}
	if _, err := io.ReadFull(connection, reply); err != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	value := reply[0]
	clear(reply)
	if err := ValidateStreamReply(kind, value); err != nil {
		return nil, 0, &RenewalRequired{Reason: RenewalGatewayRejected}
	}
	if ctx.Err() != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	keepOpen = true
	return connection, value, nil
}

func protocolDeadline(ctx context.Context, now time.Time) time.Time {
	deadline := now.Add(gatewayProtocolTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}
