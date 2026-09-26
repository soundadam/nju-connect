package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/runtime"
)

func TestRunResultAsksForRenewalWithoutReauthenticating(t *testing.T) {
	err := runResult(context.Background(), &runtime.RenewalRequired{Reason: runtime.RenewalGatewayRejected})
	if err == nil || err.Error() != `renewal_required: run "soundconnect connect" to sign in again` {
		t.Fatalf("runResult() = %v", err)
	}
}

func TestRunResultSanitizesUnknownTransportFailure(t *testing.T) {
	const sensitive = "gateway-reply-secret"
	err := runResult(context.Background(), &runtime.TransportFailure{Code: runtime.FailureCode(sensitive)})
	if err == nil || err.Error() != "native transport: runtime_stopped" {
		t.Fatalf("runResult() = %v", err)
	}
}

func TestRunResultTreatsAStopAsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runResult(ctx, context.Canceled); err != nil {
		t.Fatalf("runResult() = %v", err)
	}
}

func TestCredentialRejectedErrorKeepsTheStableToken(t *testing.T) {
	err := error(&CredentialRejectedError{cause: backend.ErrCredentialRejected})
	if !strings.HasPrefix(err.Error(), "credential_rejected: ") || !errors.Is(err, backend.ErrCredentialRejected) {
		t.Fatalf("error = %q", err)
	}
}

func TestSignInWithoutAPersonReportsTheFirstRejection(t *testing.T) {
	attempts := 0
	var configured config.Config
	err := signIn(context.Background(), Deps{}, &configured, func() error {
		attempts++
		return backend.ErrCredentialRejected
	})
	var rejected *CredentialRejectedError
	if attempts != 1 || !errors.As(err, &rejected) {
		t.Fatalf("attempts = %d, err = %v", attempts, err)
	}
}
