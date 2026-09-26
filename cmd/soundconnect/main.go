package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/soundadam/soundconnect/internal/app"
)

var version = "dev"

func main() {
	os.Exit(run(productionDeps(), os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches one invocation and returns its exit status.
func run(deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		return runConnect(deps, nil, stdout, stderr)
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		writeUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "soundconnect %s\n", version)
		return 0
	case "setup":
		return exitStatus(runSetup(deps, arguments[1:], stdout, stderr), stderr)
	case "account":
		return exitStatus(runAccount(deps, arguments[1:], stdout, stderr), stderr)
	case "configure":
		return exitStatus(runConfigure(deps, arguments[1:], stdout, stderr), stderr)
	case "backends":
		return exitStatus(runBackends(deps, arguments[1:], stdout, stderr), stderr)
	case "auth-info":
		return exitStatus(runAuthInfo(deps, arguments[1:], stdout, stderr), stderr)
	case "migrate":
		return exitStatus(runMigrate(deps, arguments[1:], stdout, stderr), stderr)
	case "doctor":
		return exitStatus(runDoctor(deps, arguments[1:], stdout, stderr), stderr)
	case "connect":
		return runConnect(deps, arguments[1:], stdout, stderr)
	case "disconnect":
		return exitStatus(runDisconnect(deps, arguments[1:], stdout, stderr), stderr)
	case "logout":
		return exitStatus(runLogout(deps, arguments[1:], stdout, stderr), stderr)
	case "dry-run":
		return exitStatus(runDryRun(deps, arguments[1:], stdout, stderr), stderr)
	case "status":
		return exitStatus(runStatus(deps, arguments[1:], stdout, stderr), stderr)
	case "speedtest":
		return runSpeedtest(deps, arguments[1:], stdout, stderr)
	case app.BackgroundRuntimeCommand:
		return runBackgroundRuntime(deps, arguments[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		writeUsage(stderr)
		return 2
	}
}

func runDryRun(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect dry-run", flag.ContinueOnError)
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	return dryRun(context.Background(), withInteraction(deps, app.LineOptions{}, stderr), stdout)
}

// dryRun renders as much of the dry-run report as the gateway allowed.
func dryRun(ctx context.Context, deps app.Deps, stdout io.Writer) error {
	report, err := app.DryRun(ctx, deps)
	if report.Accepted {
		fmt.Fprintln(stdout, "authentication: accepted")
	}
	if report.SessionRetained {
		fmt.Fprintln(stdout, "session: retained_in_memory")
	}
	if bootstrap := report.Bootstrap; bootstrap != nil {
		fmt.Fprintln(stdout, "initialization: gateway_bootstrap_available")
		fmt.Fprintf(stdout, "resources: web=%d tcp=%d l3vpn=%d unknown=%d\n",
			bootstrap.Resources.Web, bootstrap.Resources.TCP, bootstrap.Resources.L3VPN, bootstrap.Resources.Unknown)
		fmt.Fprintf(stdout, "services: tcp_required=%t l3vpn_required=%t local_agent_required=%t\n",
			bootstrap.Services.TCP, bootstrap.Services.L3VPN, bootstrap.Services.LocalAgentRequired())
		fmt.Fprintf(stdout, "policies: internal_dns=%t dedicated_line=%t security_check=%t\n",
			bootstrap.Services.InternalDNS, bootstrap.Services.DedicatedLine, bootstrap.Services.SecurityCheck)
	}
	if plan := report.Plan; plan != nil {
		fmt.Fprintf(stdout, "handoff: mode=%s ready=%t\n", plan.Mode, plan.BoundaryReady)
		fmt.Fprintln(stdout, "dataplane: not_started")
	}
	return err
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
