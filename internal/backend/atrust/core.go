// Package atrustbackend is nju-connect's aTrust backend.
//
// nju-connect owns the interfaces in this file, the resource model and
// routing in resources.go and router.go, the lifecycle in runtime.go, and the
// OAuth callback parser. The wire protocol lives behind Core; NewCore in
// zjuconnect.go supplies it by adapting the pinned AGPL-3.0
// github.com/mythologyli/zju-connect client.
package atrustbackend

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/soundadam/nju-connect/internal/backend"
)

// ErrFactorUnavailable is returned by a Prompter that cannot supply the
// requested authentication factor. A Core must surface it (wrapped or not)
// instead of retrying the same factor.
var ErrFactorUnavailable = errors.New("aTrust authentication factor is unavailable")

// ErrSessionExpired is returned by Core.Resume when saved client data is no
// longer accepted by the gateway. The caller then discards the saved data and
// falls back to Core.Authenticate.
var ErrSessionExpired = errors.New("saved aTrust session has expired")

// Core is the aTrust protocol boundary.
// Implementations must verify the gateway certificate against
// Endpoint.Host while connecting to Endpoint.DialHost(), must never read
// standard input or open a browser themselves, and must not log credentials,
// codes, cookies, or client data.
type Core interface {
	// Discover reads the gateway's public, unauthenticated login
	// configuration. It must not start a login, send a verification code, or
	// change device trust.
	Discover(ctx context.Context, endpoint backend.Endpoint) ([]backend.AuthenticationMethod, error)

	// Authenticate performs an interactive login with the selected method,
	// requesting each factor the gateway demands through prompter.
	Authenticate(ctx context.Context, request LoginRequest, prompter Prompter) (Session, error)

	// Resume re-establishes a session from opaque client data previously
	// returned by Session.ClientData, without prompting. It returns
	// ErrSessionExpired when the gateway rejects the saved state.
	Resume(ctx context.Context, request ResumeRequest) (Session, error)
}

// DialFunc opens the network connections a Core makes to the gateway and its
// tunnel nodes. nju-connect supplies one that honors the configured
// upstream proxy; a nil DialFunc means a plain net.Dialer.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// LoginRequest selects the gateway, the discovered authentication method, and
// the non-secret account name. Secrets arrive only through the Prompter.
type LoginRequest struct {
	Endpoint backend.Endpoint
	Method   backend.AuthenticationMethod
	Username string
	Dial     DialFunc
}

// ResumeRequest carries saved client data. The Core must not retain
// ClientData after Resume returns; the caller clears it.
type ResumeRequest struct {
	Endpoint   backend.Endpoint
	ClientData []byte
	Dial       DialFunc
}

// Session is an authenticated aTrust session. Methods may be called
// concurrently with Tunnel use; Close releases everything the session owns.
type Session interface {
	// ClientData returns opaque bytes that let Resume restore this session.
	// The caller persists them in the aTrust client-data store and clears the
	// returned slice; implementations must return a fresh copy each call.
	ClientData() ([]byte, error)

	// Resources returns the resources this account may reach through the
	// tunnel, translated into nju-connect's resource model.
	Resources(ctx context.Context) (Resources, error)

	// OpenTunnel selects a tunnel node and returns a TCP tunnel bound to this
	// session.
	OpenTunnel(ctx context.Context) (Tunnel, error)

	Close() error
}

// Tunnel carries TCP connections to authorized resources.
type Tunnel interface {
	// DialTCP opens one TCP connection to destination through the tunnel.
	// The returned connection must honor deadlines and Close.
	DialTCP(ctx context.Context, destination *net.TCPAddr) (net.Conn, error)

	// Run maintains the tunnel (keepalive, node health) until ctx is
	// cancelled or the tunnel fails. A nil return means ctx ended.
	Run(ctx context.Context) error

	Close() error
}

// Prompter supplies interactive authentication factors. The host (CLI or
// GUI) implements it; the Core never reads standard input directly. Returned
// secret byte slices are owned by the Core, which clears them after use.
// Each method returns ErrFactorUnavailable when the host cannot supply the
// factor, or ctx.Err() when the user cancels.
type Prompter interface {
	Password(ctx context.Context, request PasswordRequest) ([]byte, error)
	VerificationCode(ctx context.Context, request VerificationRequest) ([]byte, error)
	Captcha(ctx context.Context, challenge CaptchaChallenge) (string, error)
	OAuthCode(ctx context.Context, request OAuthRequest) (string, error)
}

// PasswordRequest asks for the long-lived account password.
type PasswordRequest struct {
	Username    string
	LoginDomain string
}

// VerificationRequest asks for a one-time code the gateway has already sent.
type VerificationRequest struct {
	// Channel names the delivery channel, for example "sms".
	Channel string
	// Destination is the masked destination as displayed by the gateway,
	// for example "138****0000". It may be empty.
	Destination string
	// ResendAfter is the gateway-advertised resend delay, when known.
	ResendAfter time.Duration
}

// CaptchaChallenge carries a graphical captcha image for the user to solve.
type CaptchaChallenge struct {
	Image    []byte
	MIMEType string
}

// OAuthRequest asks the host to complete a browser login and return the
// authorization code from the gateway callback. Hosts validate the callback
// with ParseOAuthCallbackCode against Endpoint before returning the code.
type OAuthRequest struct {
	Endpoint backend.Endpoint
	LoginURL string
}

// PrompterFuncs adapts optional functions to Prompter. A nil function
// reports ErrFactorUnavailable.
type PrompterFuncs struct {
	OnPassword         func(context.Context, PasswordRequest) ([]byte, error)
	OnVerificationCode func(context.Context, VerificationRequest) ([]byte, error)
	OnCaptcha          func(context.Context, CaptchaChallenge) (string, error)
	OnOAuthCode        func(context.Context, OAuthRequest) (string, error)
}

func (funcs PrompterFuncs) Password(ctx context.Context, request PasswordRequest) ([]byte, error) {
	if funcs.OnPassword == nil {
		return nil, ErrFactorUnavailable
	}
	return funcs.OnPassword(ctx, request)
}

func (funcs PrompterFuncs) VerificationCode(ctx context.Context, request VerificationRequest) ([]byte, error) {
	if funcs.OnVerificationCode == nil {
		return nil, ErrFactorUnavailable
	}
	return funcs.OnVerificationCode(ctx, request)
}

func (funcs PrompterFuncs) Captcha(ctx context.Context, challenge CaptchaChallenge) (string, error) {
	if funcs.OnCaptcha == nil {
		return "", ErrFactorUnavailable
	}
	return funcs.OnCaptcha(ctx, challenge)
}

func (funcs PrompterFuncs) OAuthCode(ctx context.Context, request OAuthRequest) (string, error) {
	if funcs.OnOAuthCode == nil {
		return "", ErrFactorUnavailable
	}
	return funcs.OnOAuthCode(ctx, request)
}

// Discovery runs public sign-in method discovery, defaulting to NewCore.
type Discovery struct {
	Core Core
}

func (discovery Discovery) Discover(ctx context.Context, endpoint backend.Endpoint) ([]backend.AuthenticationMethod, error) {
	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}
	core := discovery.Core
	if core == nil {
		core = NewCore()
	}
	return core.Discover(ctx, endpoint)
}

func validateEndpoint(endpoint backend.Endpoint) error {
	if endpoint.Host == "" {
		return errors.New("aTrust gateway host is required")
	}
	if endpoint.Port < 1 || endpoint.Port > 65535 {
		return errors.New("aTrust gateway port is invalid")
	}
	return nil
}
