package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/soundadam/nju-connect/internal/app"
	"github.com/soundadam/nju-connect/internal/backend/atrust"
)

var version = "dev"

func main() {
	atrustbackend.SetUpstreamDebugLog(upstreamDebugLog(os.Getenv, os.Stderr))
	os.Exit(run(productionDeps(), os.Args[1:], os.Stdout, os.Stderr))
}

// upstreamDebugLog is where the linked aTrust core's own log lines go:
// stderr with NJU_CONNECT_DEBUG=1, nowhere otherwise, so they never break
// the stderr contract or interleave with a form.
func upstreamDebugLog(getenv func(string) string, stderr io.Writer) io.Writer {
	if getenv("NJU_CONNECT_DEBUG") == "1" {
		return stderr
	}
	return nil
}

// run dispatches one invocation and returns its exit status.
func run(deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), deps, arguments, stdout, stderr)
}

func runContext(ctx context.Context, deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	root := newRootCommand(deps)
	root.SetOut(stdout)
	root.SetErr(stderr)
	// A nil slice would make Cobra fall back to os.Args.
	root.SetArgs(append([]string{}, arguments...))
	return exitStatus(root.ExecuteContext(ctx), stderr)
}

const rootUsage = `usage: nju-connect [command] [flags]

With no command, nju-connect runs connect.

commands:
  setup      configure backend, account, and long-lived password
  account    show or change the saved account, password and aTrust session
  configure  switch non-secret backend and listener settings
  backends   print non-secret backend metadata and capabilities
  auth-info  discover public aTrust authentication methods without logging in
  connect    authenticate and run the native userspace VPN core (default)
  disconnect stop the active native userspace VPN core
  logout     clear saved aTrust session and OAuth browser state
  dry-run    authenticate and validate gateway handoff without starting dataplane
  status     print sanitized runtime status
  speedtest  measure the NJU campus IPv4 path
  doctor     inspect the local nju-connect configuration
  version    print build identity

Run "nju-connect <command> -h" for command flags.`

// newRootCommand builds the command tree. Every command writes through
// cmd.OutOrStdout and cmd.ErrOrStderr and returns an error that exitStatus
// maps to the exit status; Cobra itself prints nothing.
func newRootCommand(deps app.Deps) *cobra.Command {
	root := &cobra.Command{
		Use:  "nju-connect",
		Long: rootUsage,
		Args: func(cmd *cobra.Command, arguments []string) error {
			if len(arguments) > 0 {
				return withHelp(cmd, app.Usagef("unknown command %q", arguments[0]))
			}
			return nil
		},
		// No command means connect with no flags.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConnect(cmd, deps, connectOptions{})
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{
		Use:    "help [command]",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, arguments []string) error {
			target, rest, err := cmd.Root().Find(arguments)
			if err != nil || len(rest) > 0 {
				return withHelp(cmd.Root(), app.Usagef("unknown command %q", arguments[0]))
			}
			return target.Help()
		},
	})
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) { writeHelp(cmd.OutOrStdout(), cmd) })
	root.SetFlagErrorFunc(flagError)

	root.AddCommand(
		newSetupCommand(deps),
		newAccountCommand(deps),
		newConfigureCommand(deps),
		newBackendsCommand(),
		newAuthInfoCommand(deps),
		newConnectCommand(deps),
		newDisconnectCommand(deps),
		newLogoutCommand(deps),
		newDryRunCommand(deps),
		newStatusCommand(deps),
		newSpeedtestCommand(deps),
		newDoctorCommand(deps),
		&cobra.Command{
			Use: "version",
			RunE: func(cmd *cobra.Command, _ []string) error {
				fmt.Fprintf(cmd.OutOrStdout(), "nju-connect %s\n", version)
				return nil
			},
		},
		newBackgroundRuntimeCommand(deps),
	)
	finishCommands(root)
	return root
}

func newDryRunCommand(deps app.Deps) *cobra.Command {
	return &cobra.Command{
		Use: "dry-run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr())
			return dryRun(cmd.Context(), deps, cmd.OutOrStdout())
		},
	}
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
