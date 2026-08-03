package runtime

import (
	"errors"
	"time"
)

// FailureStage is a fixed, secret-free description of where a native command
// establishment attempt stopped. It deliberately carries no underlying error.
type FailureStage string

const (
	StageUpstreamConnectFailed        FailureStage = "upstream_connect_failed"
	StageProtocolTLSHandshakeFailed   FailureStage = "protocol_tls_handshake_failed"
	StageProtocolTLSCertificateFailed FailureStage = "protocol_tls_certificate_failed"
	StageProtocolPrefaceFailed        FailureStage = "protocol_preface_failed"
	StageSendIPWriteFailed            FailureStage = "send_ip_write_failed"
	StageSendIPReadFailed             FailureStage = "send_ip_read_failed"
	StageSendIPRejected               FailureStage = "send_ip_rejected"
	StageRXHandshakeFailed            FailureStage = "rx_handshake_failed"
	StageTXHandshakeFailed            FailureStage = "tx_handshake_failed"
	StageRXStreamClosed               FailureStage = "rx_stream_closed"
	StageRXInvalidIPv4                FailureStage = "rx_invalid_ipv4"
	StageTXStreamClosed               FailureStage = "tx_stream_closed"
)

// CommandFailure is safe to publish to CLI and UI observers. Attempt is
// session-local and contains no gateway identity.
type CommandFailure struct {
	Attempt uint64
	Stage   FailureStage
	At      time.Time
}

type stageFailure struct {
	stage FailureStage
	cause error
}

func newStageFailure(stage FailureStage, cause error) error {
	if !validFailureStage(stage) {
		stage = StageProtocolTLSHandshakeFailed
	}
	if cause != ErrGatewayRejected {
		cause = nil
	}
	return &stageFailure{stage: stage, cause: cause}
}

func (failure *stageFailure) Error() string {
	if failure == nil || !validFailureStage(failure.stage) {
		return string(FailureRuntimeStopped)
	}
	return string(failure.stage)
}

func (failure *stageFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func failureStageOf(err error) (FailureStage, bool) {
	var failure *stageFailure
	if !errors.As(err, &failure) || failure == nil || !validFailureStage(failure.stage) {
		return "", false
	}
	return failure.stage, true
}

func validFailureStage(stage FailureStage) bool {
	switch stage {
	case StageUpstreamConnectFailed,
		StageProtocolTLSHandshakeFailed,
		StageProtocolTLSCertificateFailed,
		StageProtocolPrefaceFailed,
		StageSendIPWriteFailed,
		StageSendIPReadFailed,
		StageSendIPRejected,
		StageRXHandshakeFailed,
		StageTXHandshakeFailed,
		StageRXStreamClosed,
		StageRXInvalidIPv4,
		StageTXStreamClosed:
		return true
	default:
		return false
	}
}
