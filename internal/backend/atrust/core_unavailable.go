package atrustbackend

import (
	"context"

	"github.com/soundadam/soundconnect/internal/backend"
)

// NewCore returns the aTrust protocol core linked into this build. The
// clean-room implementation replaces this constructor; until then every
// operation fails with ErrProtocolNotImplemented.
func NewCore() Core { return unavailableCore{} }

type unavailableCore struct{}

func (unavailableCore) Discover(context.Context, backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	return nil, ErrProtocolNotImplemented
}

func (unavailableCore) Authenticate(context.Context, LoginRequest, Prompter) (Session, error) {
	return nil, ErrProtocolNotImplemented
}

func (unavailableCore) Resume(context.Context, ResumeRequest) (Session, error) {
	return nil, ErrProtocolNotImplemented
}
