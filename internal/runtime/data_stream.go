package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

const gatewayProtocolTimeout = 15 * time.Second

type AuthenticatedDataStreamOpener struct {
	profile   ProtocolProfile
	now       func() time.Time
	onFailure func(FailureStage)
}

func NewAuthenticatedDataStreamOpener(profile ProtocolProfile, onFailure func(FailureStage)) (*AuthenticatedDataStreamOpener, error) {
	if profile == nil {
		return nil, errors.New("authenticated data stream profile is required")
	}
	return &AuthenticatedDataStreamOpener{profile: profile, now: time.Now, onFailure: onFailure}, nil
}

func (opener *AuthenticatedDataStreamOpener) Open(ctx context.Context, kind StreamKind, token sessiontoken.NativeGatewayToken, assigned netip.Addr) (io.ReadWriteCloser, byte, error) {
	if _, err := ExpectedStreamReply(kind); err != nil {
		return nil, 0, err
	}
	if len(token) != agentTokenSize || !assigned.Is4() {
		return nil, 0, &TransportFailure{Code: FailureProtocolInvalid}
	}
	failed := true
	defer func() {
		if failed && ctx.Err() == nil && opener.onFailure != nil {
			opener.onFailure(dataHandshakeFailureStage(kind))
		}
	}()
	connection, err := opener.profile.Dial(ctx)
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
	if err := opener.profile.WriteInitialDataRequest(connection, request); err != nil {
		clear(request)
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	clear(request)
	reply, err := opener.profile.ReadInitialDataReply(connection)
	if err != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	expected, _ := ExpectedStreamReply(kind)
	if reply != uint32(expected) {
		return nil, 0, &RenewalRequired{Reason: RenewalGatewayRejected}
	}
	if ctx.Err() != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, 0, &TransportFailure{Code: FailureTransportUnavailable}
	}
	keepOpen = true
	failed = false
	return connection, expected, nil
}

func dataHandshakeFailureStage(kind StreamKind) FailureStage {
	if kind == StreamTX {
		return StageTXHandshakeFailed
	}
	return StageRXHandshakeFailed
}

func protocolDeadline(ctx context.Context, now time.Time) time.Time {
	deadline := now.Add(gatewayProtocolTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		return contextDeadline
	}
	return deadline
}
