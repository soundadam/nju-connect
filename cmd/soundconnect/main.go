package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/auth"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/credential"
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
		return exitStatus(runSetup(arguments[1:], stdout, stderr), stderr)
	case "account":
		return exitStatus(runAccount(arguments[1:], stdout, stderr), stderr)
	case "configure":
		return exitStatus(runConfigure(arguments[1:], stdout, stderr), stderr)
	case "backends":
		return exitStatus(runBackends(arguments[1:], stdout, stderr), stderr)
	case "auth-info":
		return exitStatus(runAuthInfo(arguments[1:], stdout, stderr), stderr)
	case "migrate":
		return exitStatus(runMigrate(arguments[1:], stdout, stderr), stderr)
	case "doctor":
		return exitStatus(runDoctor(arguments[1:], stdout, stderr), stderr)
	case "connect":
		return connectCommand(arguments[1:], stdout, stderr)
	case "disconnect":
		return exitStatus(runDisconnect(arguments[1:], stdout, stderr), stderr)
	case "logout":
		return exitStatus(runLogout(arguments[1:], stdout, stderr), stderr)
	case "dry-run":
		return dryRunCommand(arguments[1:], stdout, stderr)
	case "status":
		return exitStatus(runStatus(arguments[1:], stdout, stderr), stderr)
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
	configured, err := loadProfile(context.Background(), paths, true, stderr)
	if err != nil {
		return exitStatus(err, stderr)
	}
	password, err := readSavedPassword(paths, configured)
	if err != nil {
		fmt.Fprintln(stderr, err)
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

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `usage: soundconnect [command] [flags]

With no command, soundconnect runs connect.

commands:
  setup      configure backend, account, and long-lived password
  account    show or change the saved account, password and aTrust session
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
