package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/atrust"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
	setupservice "github.com/soundadam/soundconnect/internal/setup"
)

var version = "dev"

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
		return exitStatus(runBackends(arguments[1:], stdout, stderr), stderr)
	case "auth-info":
		return exitStatus(runAuthInfo(arguments[1:], stdout, stderr), stderr)
	case "migrate":
		return runMigrate(arguments[1:], stdout, stderr)
	case "doctor":
		return exitStatus(runDoctor(arguments[1:], stdout, stderr), stderr)
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
		code, promptErr := app.NewLineInteraction(app.LineOptions{Input: os.Stdin, Output: stderr}).
			VerificationCode(context.Background(), "")
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
		*authType = app.ATrustPasswordAuthType
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
		if configured.AuthType == app.ATrustPasswordAuthType && strings.TrimSpace(configured.Username) == "" {
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
	if backendName == backend.EasyConnect || configured.AuthType == app.ATrustPasswordAuthType {
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
		if configured.AuthType == app.ATrustPasswordAuthType {
			credentialStatus = "system_store_password"
		}
	}
	fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\ncredential: %s\n", paths.Config, backendName, credentialStatus)
	return 0
}

// chooseATrustSetupMethod selects the aTrust authentication method from the
// gateway's advertised methods.
func chooseATrustSetupMethod(configured *config.Config, authType, loginDomain string, stderr io.Writer) int {
	endpoint, err := app.ParseATrustEndpoint(configured.Server)
	if err != nil {
		fmt.Fprintf(stderr, "parse aTrust gateway: %v\n", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), app.ATrustDiscoveryTimeout)
	methods, err := (atrustbackend.Discovery{Core: newATrustCore()}).Discover(ctx, endpoint)
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "discover aTrust authentication: %v\n", err)
		return 1
	}
	selected, err := app.SelectATrustAuthenticationMethod(methods, authType, loginDomain)
	if err != nil {
		fmt.Fprintf(stderr, "setup failed: %v\n", err)
		return 1
	}
	if err := app.ValidateATrustAuthenticationType(selected.Type); err != nil {
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
