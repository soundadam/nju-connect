package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFailureStagesAreFixedAndNeverRetainRawErrors(t *testing.T) {
	for _, stage := range []FailureStage{
		StageUpstreamConnectFailed,
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
		StageTXStreamClosed,
	} {
		err := newStageFailure(stage, errors.New("secret token and gateway reply"))
		got, ok := failureStageOf(err)
		if !ok || got != stage || err.Error() != string(stage) {
			t.Fatalf("stage error = %q, %q, %t", err, got, ok)
		}
		if strings.Contains(err.Error(), "secret") || errors.Unwrap(err) != nil {
			t.Fatalf("stage %q retained a raw cause", stage)
		}
	}
}

func TestSendIPRejectionWrapsOnlyFixedRenewalSentinel(t *testing.T) {
	err := newStageFailure(StageSendIPRejected, ErrGatewayRejected)
	if !errors.Is(err, ErrGatewayRejected) || err.Error() != string(StageSendIPRejected) {
		t.Fatalf("rejection stage = %v", err)
	}
	wrapped := newStageFailure(StageSendIPRejected, fmt.Errorf("secret: %w", ErrGatewayRejected))
	if errors.Is(wrapped, ErrGatewayRejected) || strings.Contains(wrapped.Error(), "secret") {
		t.Fatalf("rejection retained a wrapped raw error: %v", wrapped)
	}
}
