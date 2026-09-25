package main

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

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/dial"
	"github.com/soundadam/soundconnect/internal/runtime"
)

const atrustGatewayDialTimeout = 15 * time.Second

// newATrustCore is the protocol core linked into this build. Tests replace it.
var newATrustCore = atrustbackend.NewCore

// reportATrustError prints a user-facing aTrust failure and returns the exit
// code.
func reportATrustError(stderr io.Writer, action string, err error) int {
	fmt.Fprintf(stderr, "%s: %v\n", action, err)
	return 1
}

func runATrustConnectContext(
	ctx context.Context,
	paths config.Paths,
	configured config.Config,
	verificationCodeStdin bool,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if err := validateATrustAuthenticationType(configured.AuthType); err != nil {
		fmt.Fprintf(stderr, "connect aTrust backend: %v\n", err)
		return 2
	}
	endpoint, err := parseATrustEndpoint(configured.Server)
	if err != nil {
		fmt.Fprintf(stderr, "parse aTrust gateway: %v\n", err)
		return 2
	}
	gatewayDial, err := dial.New(configured.UpstreamProxy, atrustGatewayDialTimeout)
	if err != nil {
		fmt.Fprintf(stderr, "prepare aTrust transport: %v\n", err)
		return 1
	}
	core := newATrustCore()

	method := backend.AuthenticationMethod{Type: configured.AuthType, Domain: configured.LoginDomain}
	if method.Domain == "" || method.Type == atrustOAuthAuthType {
		// The tenant login domain and OAuth login URL are gateway-issued, so
		// they are discovered rather than stored or hard-coded.
		methods, discoverErr := (atrustbackend.Discovery{Core: core}).Discover(ctx, endpoint)
		if discoverErr != nil {
			return reportATrustError(stderr, "discover aTrust authentication", discoverErr)
		}
		method, err = selectATrustAuthenticationMethod(methods, configured.AuthType, configured.LoginDomain)
		if err != nil {
			fmt.Fprintf(stderr, "select aTrust authentication: %v\n", err)
			return 1
		}
	}

	clientDataStore, err := newATrustClientDataStore(paths.ATrustClientData)
	if err != nil {
		fmt.Fprintf(stderr, "prepare aTrust session store: %v\n", err)
		return 1
	}
	savedClientData, err := clientDataStore.Get()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, credential.ErrEmptyCredential) {
			fmt.Fprintf(stderr, "read aTrust session: %v\n", err)
			return 1
		}
		savedClientData = nil
	}

	connection, err := atrustbackend.Connect(ctx, atrustbackend.ConnectConfig{
		Core: core,
		Login: atrustbackend.LoginRequest{
			Endpoint: endpoint,
			Method:   method,
			Username: configured.Username,
			Dial:     gatewayDial,
		},
		SavedClientData: savedClientData,
		Prompter:        newATrustCLIPrompter(paths, verificationCodeStdin, os.Stdin, stderr),
		SOCKSListen:     configured.SOCKSListen,
	})
	credential.Clear(savedClientData)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}
		return reportATrustError(stderr, "connect aTrust backend", err)
	}
	defer connection.Close()

	if clientData, dataErr := connection.ClientData(); dataErr == nil && len(clientData) > 0 {
		if storeErr := clientDataStore.Set(clientData); storeErr != nil {
			fmt.Fprintf(stderr, "warning: save aTrust session: %v\n", storeErr)
		}
		credential.Clear(clientData)
	}

	statusTracker := newRuntimeStatusTracker(runtime.ProfileATrustTCP)
	statusServer, err := startRuntimeStatusServer(runtimeStatusPath(paths.Root), func() runtimeStatusSnapshot {
		statusTracker.UpdateIngressTraffic(connection.Traffic(), time.Now())
		return statusTracker.Snapshot()
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare runtime status: %v\n", err)
		return 1
	}
	defer statusServer.Close()
	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	statusServer.SetStop(cancelRuntime)

	listen := connection.SOCKSAddr().String()
	statusObserver := runtimeStatusObserver(nativeCLIObserver(stdout), statusTracker)
	fmt.Fprintln(stdout, "backend: atrust")
	if connection.Resumed() {
		fmt.Fprintln(stdout, "authentication: resumed")
	} else {
		fmt.Fprintln(stdout, "authentication: accepted")
	}
	statusObserver.SOCKSListening(listen)
	statusObserver.AccessEvidence(true)
	statusObserver.StateChanged(nativeapp.StateConnected)
	if err := connection.Run(runtimeContext); err != nil {
		statusObserver.AccessEvidence(false)
		fmt.Fprintf(stderr, "aTrust transport: %v\n", err)
		return 1
	}
	return 0
}

// newATrustCLIPrompter supplies interactive factors from the terminal, the
// Keychain, and the bundled OAuth helper. The protocol core never reads
// standard input itself.
func newATrustCLIPrompter(paths config.Paths, verificationCodeStdin bool, input *os.File, output io.Writer) atrustbackend.Prompter {
	return atrustbackend.PrompterFuncs{
		OnPassword: func(context.Context, atrustbackend.PasswordRequest) ([]byte, error) {
			store, _, err := commandCredentialStore(paths)
			if err != nil {
				return nil, fmt.Errorf("open VPN password: %w", err)
			}
			password, err := store.Get()
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, credential.ErrEmptyCredential) {
				return nil, errors.New(`no saved VPN password; run "soundconnect setup --backend atrust --auth-type auth/psw" first`)
			}
			return password, err
		},
		OnVerificationCode: func(ctx context.Context, request atrustbackend.VerificationRequest) ([]byte, error) {
			if request.Destination != "" {
				fmt.Fprintf(output, "A verification code was sent to %s.\n", request.Destination)
			}
			return promptVerificationCode(ctx, input, output, verificationCodeStdin)
		},
		OnCaptcha: func(context.Context, atrustbackend.CaptchaChallenge) (string, error) {
			return "", fmt.Errorf("graphical captcha is not supported by the CLI yet: %w", atrustbackend.ErrFactorUnavailable)
		},
		OnOAuthCode: func(ctx context.Context, request atrustbackend.OAuthRequest) (string, error) {
			return promptATrustOAuthCode(ctx, request, input, output)
		},
	}
}

func promptATrustOAuthCode(ctx context.Context, request atrustbackend.OAuthRequest, input io.Reader, output io.Writer) (string, error) {
	if request.LoginURL == "" {
		return "", errors.New("aTrust OAuth login URL is unavailable")
	}
	if helperPath, available := atrustOAuthHelperPath(); available {
		return runATrustOAuthHelper(ctx, helperPath, request.LoginURL, request.Endpoint, output)
	}
	fmt.Fprintf(output, "Visit %s to sign in.\n", request.LoginURL)
	fmt.Fprintln(output, "Paste the resulting /passport/v1/auth/httpsOauth2 callback URL here; it stays local.")
	fmt.Fprint(output, "Callback URL: ")

	type readResult struct {
		line string
		err  error
	}
	completed := make(chan readResult, 1)
	go func() {
		line, readErr := readBoundedLine(input, 8192)
		completed <- readResult{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-completed:
		if result.err != nil {
			return "", result.err
		}
		return atrustbackend.ParseOAuthCallbackCode(result.line, request.Endpoint)
	}
}

// atrustOAuthHelperPath locates the bundled WebKit OAuth helper next to the
// CLI, or an absolute SOUNDCONNECT_ATRUST_OAUTH_HELPER override.
func atrustOAuthHelperPath() (string, bool) {
	if configured := os.Getenv("SOUNDCONNECT_ATRUST_OAUTH_HELPER"); configured != "" {
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
	candidate := filepath.Join(filepath.Dir(executable), "soundconnect-atrust-oauth-helper")
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
	fmt.Fprintln(output, "Opening SoundConnect OAuth login; complete NJU login there if the saved session has expired.")
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

func readBoundedLine(input io.Reader, maximum int) (string, error) {
	if input == nil {
		return "", errors.New("callback input is unavailable")
	}
	line := make([]byte, 0, 256)
	defer clear(line)
	var one [1]byte
	for {
		count, err := input.Read(one[:])
		if count > 0 {
			switch one[0] {
			case '\n':
				return string(line), nil
			case '\r':
				continue
			default:
				if len(line) >= maximum {
					return "", errors.New("callback URL is too long")
				}
				line = append(line, one[0])
			}
		}
		if err != nil {
			return "", err
		}
	}
}
