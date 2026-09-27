package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/backend/atrust"
	"github.com/soundadam/nju-connect/internal/backend/easyconnect/session"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/dial"
	"github.com/soundadam/nju-connect/internal/runtime"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
)

const atrustGatewayDialTimeout = 15 * time.Second

func connectATrust(ctx context.Context, deps Deps, paths config.Paths, configured config.Config, events ConnectEvents) error {
	if err := ValidateATrustAuthenticationType(configured.AuthType); err != nil {
		return Usagef("connect aTrust backend: %w", err)
	}
	endpoint, err := ParseATrustEndpoint(configured.Server)
	if err != nil {
		return Usagef("parse aTrust gateway: %w", err)
	}
	gatewayDial, err := dial.New(configured.UpstreamProxy, atrustGatewayDialTimeout)
	if err != nil {
		return fmt.Errorf("prepare aTrust transport: %w", err)
	}
	core := deps.ATrustCore()

	method := backend.AuthenticationMethod{Type: configured.AuthType, Domain: configured.LoginDomain}
	if method.Domain == "" || method.Type == ATrustOAuthAuthType {
		// The tenant login domain and OAuth login URL are gateway-issued, so
		// they are discovered rather than stored or hard-coded.
		methods, discoverErr := (atrustbackend.Discovery{Core: core}).Discover(ctx, endpoint)
		if discoverErr != nil {
			return fmt.Errorf("discover aTrust authentication: %w", discoverErr)
		}
		method, err = SelectATrustAuthenticationMethod(methods, configured.AuthType, configured.LoginDomain)
		if err != nil {
			return fmt.Errorf("select aTrust authentication: %w", err)
		}
	}

	clientDataStore, err := deps.ATrustSessionStore(ATrustSessionLocation(paths, configured.CredentialStore))
	if err != nil {
		return fmt.Errorf("prepare aTrust session store: %w", err)
	}
	var connection *atrustbackend.Connection
	err = signIn(ctx, deps, &configured, func() error {
		savedClientData, err := clientDataStore.Get()
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, credential.ErrEmptyCredential) {
				return fmt.Errorf("read aTrust session: %w", err)
			}
			savedClientData = nil
		}
		defer credential.Clear(savedClientData)
		connection, err = atrustbackend.Connect(ctx, atrustbackend.ConnectConfig{
			Core: core,
			Login: atrustbackend.LoginRequest{
				Endpoint: endpoint,
				Method:   method,
				Username: configured.Username,
				Dial:     gatewayDial,
			},
			SavedClientData: savedClientData,
			Prompter:        newATrustPrompter(deps, paths, configured),
			SOCKSListen:     configured.SOCKSListen,
		})
		if err != nil && !errors.Is(err, backend.ErrCredentialRejected) {
			return fmt.Errorf("connect aTrust backend: %w", err)
		}
		return err
	})
	if err != nil {
		return err
	}
	defer connection.Close()

	if clientData, dataErr := connection.ClientData(); dataErr == nil && len(clientData) > 0 {
		if storeErr := clientDataStore.Set(clientData); storeErr != nil {
			fmt.Fprintf(deps.diagnostics(), "warning: save aTrust session: %v\n", storeErr)
		}
		credential.Clear(clientData)
	}

	statusTracker := NewRuntimeStatusTracker(runtime.ProfileATrustTCP)
	statusServer, err := runtimecontrol.Serve(runtimecontrol.Path(paths.Root), func() runtimecontrol.Snapshot {
		statusTracker.UpdateIngressTraffic(connection.Traffic(), time.Now())
		return statusTracker.Snapshot()
	})
	if err != nil {
		return fmt.Errorf("prepare runtime status: %w", err)
	}
	defer statusServer.Close()
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	statusServer.SetStop(cancelRuntime)

	statusObserver := RuntimeStatusObserver(events.Runtime, statusTracker)
	events.authenticated(backend.ATrust, connection.Resumed())
	statusObserver.SOCKSListening(connection.SOCKSAddr().String())
	statusObserver.AccessEvidence(true)
	statusObserver.StateChanged(nativeapp.StateConnected)
	if err := connection.Run(runtimeContext); err != nil {
		statusObserver.AccessEvidence(false)
		return fmt.Errorf("aTrust transport: %w", err)
	}
	return nil
}

// newATrustPrompter supplies interactive factors from the user, the saved
// password, and the bundled OAuth helper. The protocol core never reads
// standard input itself.
func newATrustPrompter(deps Deps, paths config.Paths, configured config.Config) atrustbackend.Prompter {
	return atrustbackend.PrompterFuncs{
		OnPassword: func(context.Context, atrustbackend.PasswordRequest) ([]byte, error) {
			return readSavedPassword(deps, paths, configured)
		},
		OnVerificationCode: func(ctx context.Context, request atrustbackend.VerificationRequest) ([]byte, error) {
			return deps.Interaction.VerificationCode(ctx, request.Destination)
		},
		OnCaptcha: func(context.Context, atrustbackend.CaptchaChallenge) (string, error) {
			return "", fmt.Errorf("graphical captcha is not supported by the CLI yet: %w", atrustbackend.ErrFactorUnavailable)
		},
		OnOAuthCode: func(ctx context.Context, request atrustbackend.OAuthRequest) (string, error) {
			return promptATrustOAuthCode(ctx, deps, request)
		},
	}
}

func promptATrustOAuthCode(ctx context.Context, deps Deps, request atrustbackend.OAuthRequest) (string, error) {
	if request.LoginURL == "" {
		return "", errors.New("aTrust OAuth login URL is unavailable")
	}
	if helperPath, available := deps.OAuthHelper(); available {
		return runATrustOAuthHelper(ctx, helperPath, request.LoginURL, request.Endpoint, deps.diagnostics())
	}
	callback, err := deps.Interaction.OAuthCallback(ctx, request.LoginURL)
	if err != nil {
		return "", err
	}
	return atrustbackend.ParseOAuthCallbackCode(callback, request.Endpoint)
}

// OAuthHelperPath locates the bundled WebKit OAuth helper next to the CLI,
// or an absolute NJU_CONNECT_ATRUST_OAUTH_HELPER override. It is the
// production Deps.OAuthHelper.
func OAuthHelperPath() (string, bool) {
	if configured := os.Getenv("NJU_CONNECT_ATRUST_OAUTH_HELPER"); configured != "" {
		if !filepath.IsAbs(configured) {
			return "", false
		}
		if info, err := os.Stat(configured); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return configured, true
		}
		return "", false
	}
	executable, err := os.Executable()
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(filepath.Dir(executable), "nju-connect-atrust-oauth-helper")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return candidate, true
	}
	return "", false
}

func runATrustOAuthHelper(
	ctx context.Context,
	helperPath string,
	loginURL string,
	endpoint backend.Endpoint,
	output io.Writer,
) (string, error) {
	fmt.Fprintln(output, "Opening nju-connect OAuth login; complete NJU login there if the saved session has expired.")
	command := exec.CommandContext(
		ctx,
		helperPath,
		"--login-url", loginURL,
		"--gateway-host", endpoint.Host,
		"--gateway-port", strconv.Itoa(endpoint.Port),
	)
	var codeOutput bytes.Buffer
	command.Stdout = &codeOutput
	command.Stderr = output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("aTrust OAuth window closed before authentication completed")
	}
	rawCode := codeOutput.Bytes()
	defer clear(rawCode)
	trimmed := bytes.TrimSpace(rawCode)
	if len(trimmed) == 0 || len(trimmed) > 4096 || bytes.ContainsAny(trimmed, " \t\r\n") {
		return "", errors.New("aTrust OAuth helper returned an invalid authorization code")
	}
	return string(trimmed), nil
}
