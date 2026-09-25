package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/doctor"
	setupservice "github.com/soundadam/soundconnect/internal/setup"
	"golang.org/x/term"
)

var version = "dev"

const atrustDiscoveryTimeout = 25 * time.Second

var (
	connectCommand = runNativeConnect
	dryRunCommand  = runDryRun
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		return connectCommand(nil, stdout, stderr)
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		writeUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "soundconnect %s\n", version)
		return 0
	case "setup":
		return runSetup(arguments[1:], stdout, stderr)
	case "configure":
		return runConfigure(arguments[1:], stdout, stderr)
	case "backends":
		return runBackends(arguments[1:], stdout, stderr)
	case "auth-info":
		return runAuthInfo(arguments[1:], stdout, stderr)
	case "migrate":
		return runMigrate(arguments[1:], stdout, stderr)
	case "doctor":
		return runDoctor(arguments[1:], stdout, stderr)
	case "connect":
		return connectCommand(arguments[1:], stdout, stderr)
	case "disconnect":
		return runDisconnect(arguments[1:], stdout, stderr)
	case "logout":
		return runLogout(arguments[1:], stdout, stderr)
	case "dry-run":
		return dryRunCommand(arguments[1:], stdout, stderr)
	case "status":
		return runStatus(arguments[1:], stdout, stderr)
	case "speedtest":
		return runSpeedtest(arguments[1:], stdout, stderr)
	case "_native-runtime":
		return runNativeRuntimeChild(arguments[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		writeUsage(stderr)
		return 2
	}
}

func runAuthInfo(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect auth-info", flag.ContinueOnError)
	backendValue := flags.String("backend", string(backend.ATrust), "protocol backend")
	server := flags.String("server", backend.DefaultATrustGateway, "aTrust gateway host or host:port")
	asJSON := flags.Bool("json", false, "write machine-readable authentication methods")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "auth-info accepts no positional arguments")
		return 2
	}
	backendName, err := backend.ParseName(*backendValue)
	if err != nil {
		fmt.Fprintf(stderr, "select protocol backend: %v\n", err)
		return 2
	}
	if backendName != backend.ATrust {
		fmt.Fprintln(stderr, "auth-info is currently available only for the aTrust backend")
		return 2
	}
	endpoint, err := parseATrustEndpoint(*server)
	if err != nil {
		fmt.Fprintf(stderr, "parse aTrust gateway: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), atrustDiscoveryTimeout)
	defer cancel()
	methods, err := (atrustbackend.Discovery{Core: newATrustCore()}).Discover(ctx, endpoint)
	if err != nil {
		return reportATrustError(stderr, "discover aTrust authentication", err)
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(methods); err != nil {
			fmt.Fprintf(stderr, "encode authentication methods: %v\n", err)
			return 1
		}
		return 0
	}
	for _, method := range methods {
		fmt.Fprintf(stdout, "backend: %s\nauth_name: %s\nauth_type: %s\nlogin_domain: %s\n",
			backendName, method.Name, method.Type, method.Domain)
	}
	return 0
}

func runMigrate(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect migrate", flag.ContinueOnError)
	legacyRoot := flags.String("from", ".", "directory containing the legacy .config state")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "migrate accepts no positional arguments")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve user configuration: %v\n", err)
		return 1
	}
	legacy, configMigrated, err := config.MigrateLegacyConfig(*legacyRoot, paths)
	if err != nil {
		fmt.Fprintf(stderr, "migrate configuration: %v\n", err)
		return 1
	}
	store, err := newSystemCredentialStore(paths.Credential)
	if err != nil {
		fmt.Fprintf(stderr, "prepare credential store: %v\n", err)
		return 1
	}
	credentialMigrated, err := credential.MigrateFile(store, legacy.Credential)
	if err != nil {
		fmt.Fprintf(stderr, "migrate credential: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "configuration_migrated: %t\ncredential_migrated: %t\nsource_preserved: true\n",
		configMigrated, credentialMigrated)
	return 0
}

func runDryRun(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect dry-run", flag.ContinueOnError)
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "dry-run accepts no positional arguments")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	configured, err := config.Load(paths.Config)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}
	passwordStore, _, err := commandCredentialStore(paths)
	if err != nil {
		fmt.Fprintf(stderr, "open credential: %v\n", err)
		return 1
	}
	password, err := passwordStore.Get()
	if err != nil {
		fmt.Fprintf(stderr, "read credential: %v\n", err)
		return 1
	}
	defer credential.Clear(password)
	client, err := gatewayauth.New(gatewayauth.Options{
		Server:      configured.Server,
		TLSInsecure: configured.TLSInsecure, UpstreamProxy: configured.UpstreamProxy,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare gateway authentication: %v\n", err)
		return 1
	}
	defer client.Close()
	result, err := client.AuthenticatePassword(context.Background(), configured.Username, password)
	if err != nil {
		fmt.Fprintf(stderr, "authenticate password: %v\n", err)
		return 1
	}
	if result.NeedsSMS() {
		if err = client.PrepareSMS(context.Background()); err != nil {
			fmt.Fprintf(stderr, "prepare verification code authentication: %v\n", err)
			return 1
		}
		code, promptErr := promptVerificationCode(context.Background(), os.Stdin, stderr, false)
		if promptErr != nil {
			if errors.Is(promptErr, context.Canceled) {
				return 0
			}
			fmt.Fprintf(stderr, "read verification code: %v\n", promptErr)
			return 1
		}
		defer credential.Clear(code)
		result, err = client.AuthenticateSMS(context.Background(), code)
		if err != nil {
			fmt.Fprintf(stderr, "authenticate verification code: %v\n", err)
			return 1
		}
	}
	if result.Accepted() {
		fmt.Fprintln(stdout, "authentication: accepted")
		session, sessionErr := client.TakeSession()
		if sessionErr != nil {
			fmt.Fprintf(stderr, "retain authenticated session: %v\n", sessionErr)
			return 1
		}
		defer session.Close()
		fmt.Fprintln(stdout, "session: retained_in_memory")
		bootstrap, probeErr := session.ProbeBootstrap(context.Background())
		if probeErr != nil {
			fmt.Fprintf(stderr, "probe gateway bootstrap: %v\n", probeErr)
			return 1
		}
		if !bootstrap.ConfigurationAvailable || !bootstrap.ResourcesAvailable {
			fmt.Fprintf(stderr, "probe gateway bootstrap: configuration_available=%t resources_available=%t\n",
				bootstrap.ConfigurationAvailable, bootstrap.ResourcesAvailable)
			return 1
		}
		fmt.Fprintln(stdout, "initialization: gateway_bootstrap_available")
		fmt.Fprintf(stdout, "resources: web=%d tcp=%d l3vpn=%d unknown=%d\n",
			bootstrap.Resources.Web, bootstrap.Resources.TCP, bootstrap.Resources.L3VPN, bootstrap.Resources.Unknown)
		fmt.Fprintf(stdout, "services: tcp_required=%t l3vpn_required=%t local_agent_required=%t\n",
			bootstrap.Services.TCP, bootstrap.Services.L3VPN, bootstrap.Services.LocalAgentRequired())
		fmt.Fprintf(stdout, "policies: internal_dns=%t dedicated_line=%t security_check=%t\n",
			bootstrap.Services.InternalDNS, bootstrap.Services.DedicatedLine, bootstrap.Services.SecurityCheck)
		plan, planErr := core.BuildDataplanePlan(session.State(), bootstrap)
		if planErr != nil {
			fmt.Fprintf(stderr, "model dataplane boundary: %v\n", planErr)
			return 1
		}
		fmt.Fprintf(stdout, "handoff: mode=%s ready=%t\n", plan.Mode, plan.BoundaryReady)
		fmt.Fprintln(stdout, "dataplane: not_started")
		return 0
	}
	if result.NextService != "" {
		fmt.Fprintf(stderr, "authentication requires unsupported next step %q (gateway code %d)\n", result.NextService, result.Code)
	} else {
		fmt.Fprintf(stderr, "authentication rejected by gateway code %d\n", result.Code)
	}
	return 1
}

func promptVerificationCode(ctx context.Context, input *os.File, output io.Writer, allowNonTerminal bool) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("verification code context is required")
	}
	if input == nil {
		return nil, credential.ErrNoTerminal
	}
	isTerminal := term.IsTerminal(int(input.Fd()))
	if !isTerminal && !allowNonTerminal {
		return nil, credential.ErrNoTerminal
	}
	if _, err := io.WriteString(output, "Verification code: "); err != nil {
		return nil, err
	}
	if isTerminal {
		original, err := term.MakeRaw(int(input.Fd()))
		if err != nil {
			return nil, err
		}
		defer term.Restore(int(input.Fd()), original) //nolint:errcheck // best-effort terminal restoration on every exit path
	}
	defer fmt.Fprintln(output)

	type readResult struct {
		code []byte
		err  error
	}
	var err error
	result := make(chan readResult, 1)
	go func() {
		code, readErr := readRawVerificationCode(input)
		select {
		case result <- readResult{code: code, err: readErr}:
		case <-ctx.Done():
			credential.Clear(code)
		}
	}()

	var rawCode []byte
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case read := <-result:
		rawCode, err = read.code, read.err
	}
	if err != nil {
		credential.Clear(rawCode)
		return nil, err
	}
	defer clear(rawCode)
	trimmed := bytes.TrimSpace(rawCode)
	if len(trimmed) == 0 {
		return nil, errors.New("verification code is required")
	}
	return append([]byte(nil), trimmed...), nil
}

func readRawVerificationCode(input io.Reader) ([]byte, error) {
	const maximumVerificationCodeBytes = 64
	code := make([]byte, 0, 8)
	defer func() {
		if code != nil {
			clear(code)
		}
	}()
	var one [1]byte
	for {
		count, err := input.Read(one[:])
		if count > 0 {
			switch one[0] {
			case 0x03:
				return nil, context.Canceled
			case '\r', '\n':
				result := append([]byte(nil), code...)
				return result, nil
			case 0x04:
				return nil, io.EOF
			case 0x08, 0x7f:
				if len(code) > 0 {
					code = code[:len(code)-1]
				}
			default:
				if len(code) >= maximumVerificationCodeBytes {
					return nil, errors.New("verification code is too long")
				}
				code = append(code, one[0])
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

func runSetup(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect setup", flag.ContinueOnError)
	backendValue := flags.String("backend", string(backend.EasyConnect), "protocol backend (easyconnect or atrust)")
	server := flags.String("server", "", "campus VPN gateway host or host:port")
	username := flags.String("username", "", "campus account")
	authType := flags.String("auth-type", "", "aTrust authentication type (auth/httpsOauth2 or auth/psw)")
	loginDomain := flags.String("login-domain", "", "aTrust login domain override")
	socksListen := flags.String("socks-listen", config.DefaultSOCKSListen, "numeric loopback SOCKS5 listener")
	upstreamProxy := flags.String("upstream-proxy", "", "optional socks5 upstream URL")
	tlsInsecure := flags.Bool("tls-insecure", false, "allow an unverified development gateway certificate")
	nativeTLSInsecure := flags.Bool("native-tls-insecure", false, "disable verification only for native protocol TLS")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from standard input without a terminal prompt")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "setup accepts no positional arguments")
		return 2
	}
	backendName, err := backend.ParseName(*backendValue)
	if err != nil {
		fmt.Fprintf(stderr, "select protocol backend: %v\n", err)
		return 2
	}
	if backendName != backend.ATrust && (strings.TrimSpace(*authType) != "" || strings.TrimSpace(*loginDomain) != "") {
		fmt.Fprintln(stderr, "auth-type and login-domain are only available for the aTrust backend")
		return 2
	}
	if *passwordStdin && backendName == backend.ATrust && strings.TrimSpace(*authType) == "" {
		// A password supplied through the private GUI pipe is an explicit
		// request for shared-password authentication. Discovery still
		// determines the tenant-specific login domain.
		*authType = atrustPasswordAuthType
	}
	lineReader := bufio.NewReader(os.Stdin)
	if strings.TrimSpace(*server) == "" {
		defaultServer := config.DefaultServer
		if backendName == backend.ATrust {
			defaultServer = config.DefaultATrustServer
		}
		value, err := promptLineDefault(lineReader, stderr, "Gateway", defaultServer)
		if err != nil {
			fmt.Fprintf(stderr, "read gateway: %v\n", err)
			return 1
		}
		*server = value
	}
	if backendName == backend.EasyConnect && strings.TrimSpace(*username) == "" {
		value, err := promptLine(lineReader, stderr, "Account: ")
		if err != nil {
			fmt.Fprintf(stderr, "read account: %v\n", err)
			return 1
		}
		*username = value
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	configured := config.Config{
		Backend:           backendName,
		Server:            *server,
		Username:          *username,
		SOCKSListen:       *socksListen,
		UpstreamProxy:     *upstreamProxy,
		TLSInsecure:       *tlsInsecure,
		NativeTLSInsecure: *nativeTLSInsecure,
	}
	if backendName == backend.ATrust {
		if code := chooseATrustSetupMethod(&configured, *authType, *loginDomain, stderr); code != 0 {
			return code
		}
		if configured.AuthType == atrustPasswordAuthType && strings.TrimSpace(configured.Username) == "" {
			value, promptErr := promptLine(lineReader, stderr, "Account: ")
			if promptErr != nil {
				fmt.Fprintf(stderr, "read account: %v\n", promptErr)
				return 1
			}
			configured.Username = value
		}
	}
	readSecret := func(prompt string) ([]byte, error) {
		if *passwordStdin {
			return readSecretLine(os.Stdin)
		}
		return credential.NewPromptStore(credential.PromptOptions{
			Input: os.Stdin, Output: stderr, Prompt: prompt,
		}).Get()
	}
	var passwordStore credential.Store
	if backendName == backend.EasyConnect || configured.AuthType == atrustPasswordAuthType {
		passwordStore, err = newSystemCredentialStore(paths.Credential)
		if err != nil {
			fmt.Fprintf(stderr, "prepare credential store: %v\n", err)
			return 1
		}
	}
	if err := setupservice.Save(paths, configured, passwordStore, readSecret); err != nil {
		fmt.Fprintf(stderr, "setup failed: %v\n", err)
		return 1
	}
	credentialStatus := "system_store"
	if backendName == backend.ATrust {
		credentialStatus = "browser_oauth"
		if configured.AuthType == atrustPasswordAuthType {
			credentialStatus = "system_store_password"
		}
	}
	fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\ncredential: %s\n", paths.Config, backendName, credentialStatus)
	return 0
}

// chooseATrustSetupMethod selects the aTrust authentication method from the
// gateway's advertised methods. Builds without a protocol core cannot
// discover, so they record the requested method (shared password by
// default) and leave the tenant login domain to connect-time discovery.
func chooseATrustSetupMethod(configured *config.Config, authType, loginDomain string, stderr io.Writer) int {
	endpoint, err := parseATrustEndpoint(configured.Server)
	if err != nil {
		fmt.Fprintf(stderr, "parse aTrust gateway: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), atrustDiscoveryTimeout)
	methods, err := (atrustbackend.Discovery{Core: newATrustCore()}).Discover(ctx, endpoint)
	cancel()
	if errors.Is(err, atrustbackend.ErrProtocolNotImplemented) {
		selected := strings.TrimSpace(authType)
		if selected == "" {
			selected = atrustPasswordAuthType
		}
		if err := validateATrustAuthenticationType(selected); err != nil {
			fmt.Fprintf(stderr, "setup failed: %v\n", err)
			return 2
		}
		configured.AuthType = selected
		configured.LoginDomain = strings.TrimSpace(loginDomain)
		fmt.Fprintln(stderr, "note: aTrust gateway discovery is unavailable in this build; the saved method is not verified")
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "discover aTrust authentication: %v\n", err)
		return 1
	}
	selected, err := selectATrustAuthenticationMethod(methods, authType, loginDomain)
	if err != nil {
		fmt.Fprintf(stderr, "setup failed: %v\n", err)
		return 1
	}
	if err := validateATrustAuthenticationType(selected.Type); err != nil {
		fmt.Fprintf(stderr, "setup failed: %v\n", err)
		return 1
	}
	configured.AuthType = selected.Type
	configured.LoginDomain = selected.Domain
	return 0
}

func readSecretLine(input io.Reader) ([]byte, error) {
	if input == nil {
		return nil, errors.New("password input is unavailable")
	}
	reader := bufio.NewReader(io.LimitReader(input, 1<<20+2))
	secret, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		credential.Clear(secret)
		return nil, errors.New("read password from standard input")
	}
	secret = bytes.TrimSuffix(secret, []byte{'\n'})
	secret = bytes.TrimSuffix(secret, []byte{'\r'})
	if len(secret) == 0 {
		credential.Clear(secret)
		return nil, credential.ErrEmptyCredential
	}
	if len(secret) > 1<<20 {
		credential.Clear(secret)
		return nil, credential.ErrCredentialTooLarge
	}
	return secret, nil
}

func promptLine(input *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := io.WriteString(output, prompt); err != nil {
		return "", err
	}
	value, err := input.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("value is required")
	}
	return value, nil
}

func promptLineDefault(input *bufio.Reader, output io.Writer, label, defaultValue string) (string, error) {
	if strings.TrimSpace(defaultValue) == "" {
		return "", errors.New("prompt default is empty")
	}
	value, err := promptLineAllowEmpty(input, output, fmt.Sprintf("%s [%s]: ", label, defaultValue))
	if err != nil {
		return "", err
	}
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func promptLineAllowEmpty(input *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := io.WriteString(output, prompt); err != nil {
		return "", err
	}
	value, err := input.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func runDoctor(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect doctor", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor accepts no positional arguments")
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	passwordStore, err := newSystemCredentialStore(paths.Credential)
	if err != nil {
		fmt.Fprintf(stderr, "prepare credential store: %v\n", err)
		return 1
	}
	report := doctor.Build(paths, passwordStore)
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode report: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "ready: %t\nconfiguration: %s\ncredential_store: %s\nupstream_proxy: %s\n",
			report.Ready, report.Configuration, report.CredentialStore, report.UpstreamProxy)
	}
	if !report.Ready {
		return 1
	}
	return 0
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `usage: soundconnect [command] [flags]

With no command, soundconnect runs connect.

commands:
  setup      configure backend, account, and long-lived password
  configure  switch non-secret backend and listener settings
  backends   print non-secret backend metadata and capabilities
  auth-info  discover public aTrust authentication methods without logging in
  migrate    import pre-release worktree configuration and credential state
  connect    authenticate and run the native userspace VPN core (default)
  disconnect stop the active native userspace VPN core
  logout     clear saved aTrust session and OAuth browser state
  dry-run    authenticate and validate gateway handoff without starting dataplane
  status     print sanitized runtime status
  speedtest  measure the NJU campus IPv4 path
  doctor     inspect the local soundconnect configuration
  version    print build identity

Run "soundconnect <command> -h" for command flags.`)
}

// parseFlags parses command flags, sending an explicitly requested help text
// to stdout with a success code while keeping parse errors on stderr.
func parseFlags(flags *flag.FlagSet, arguments []string, stdout, stderr io.Writer) (int, bool) {
	var buffered bytes.Buffer
	flags.SetOutput(&buffered)
	err := flags.Parse(arguments)
	flags.SetOutput(stderr)
	if err == nil {
		return 0, true
	}
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.Copy(stdout, &buffered)
		return 0, false
	}
	_, _ = io.Copy(stderr, &buffered)
	return 2, false
}
