package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/runtime"
)

type connectOptions struct {
	background            bool
	verificationCodeStdin bool
}

func newConnectCommand(deps app.Deps) *cobra.Command {
	var options connectOptions
	command := &cobra.Command{
		Use: "connect",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConnect(cmd, deps, options)
		},
	}
	command.Flags().BoolVar(&options.background, "background", false, "continue the native runtime as a detached process after authentication")
	command.Flags().BoolVar(&options.verificationCodeStdin, "verification-code-stdin", false, "read the verification code from standard input without requiring a terminal")
	return command
}

func runConnect(cmd *cobra.Command, deps app.Deps, options connectOptions) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stdout := cmd.OutOrStdout()
	deps = withInteraction(deps, app.LineOptions{CodeFromStdin: options.verificationCodeStdin}, cmd.ErrOrStderr())
	result, err := app.Connect(ctx, deps, app.ConnectRequest{
		Background: options.background,
		OfferSetup: !options.verificationCodeStdin,
	}, connectEvents(stdout))
	if err != nil {
		return err
	}
	if options.background {
		fmt.Fprintf(stdout, "background: pid=%d log=%s\n", result.BackgroundPID, result.LogPath)
	}
	return nil
}

// newBackgroundRuntimeCommand is the hidden command a background connect
// starts. It takes no flags or arguments; the handoff arrives on fd 3.
func newBackgroundRuntimeCommand(deps app.Deps) *cobra.Command {
	return &cobra.Command{
		Use:                app.BackgroundRuntimeCommand,
		Hidden:             true,
		DisableFlagParsing: true,
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) != 0 {
				return app.Usagef("background runtime accepts no arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			handoff, ready, err := app.BackgroundFiles()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			deps.Diagnostics = cmd.ErrOrStderr()
			err = app.RunBackground(ctx, deps, handoff, ready, connectEvents(cmd.OutOrStdout()))
			// The parent stops waiting when ready closes, so log first.
			code := exitStatus(err, cmd.ErrOrStderr())
			_ = ready.Close()
			if code != 0 {
				return exitCode(code)
			}
			return nil
		},
	}
}

// connectEvents renders connection progress as the line protocol the macOS
// app and the background log read.
func connectEvents(output io.Writer) app.ConnectEvents {
	return app.ConnectEvents{
		Authenticated: func(name backend.Name, resumed bool) {
			if name == backend.ATrust {
				fmt.Fprintln(output, "backend: atrust")
			}
			if resumed {
				fmt.Fprintln(output, "authentication: resumed")
			} else {
				fmt.Fprintln(output, "authentication: accepted")
			}
		},
		Started: func(profile runtime.ProtocolProfileMetadata) {
			fmt.Fprintf(output, "native-profile: %s\n", profile.ID)
			fmt.Fprintf(output, "native-evidence: %s\n", profile.Evidence)
			fmt.Fprintf(output, "native-security: encrypted=%t peer_verified=%t\n", profile.Security.Encrypted, profile.Security.PeerVerified)
		},
		Runtime: nativeCLIObserver(output),
	}
}

func nativeCLIObserver(output io.Writer) nativeapp.ObserverFuncs {
	return nativeapp.ObserverFuncs{
		OnState: func(state nativeapp.State) {
			fmt.Fprintf(output, "state: %s\n", state)
		},
		OnCommandFailure: func(failure nativeapp.CommandFailure) {
			fmt.Fprintf(output, "command: at=%s attempt=%d stage=%s\n",
				failure.At.UTC().Format(time.RFC3339Nano), failure.Attempt, failure.Stage)
		},
		OnDataFailure: func(stage nativeapp.DataFailureStage) {
			fmt.Fprintf(output, "data: stage=%s\n", stage)
		},
		OnSOCKSListen: func(address string) {
			fmt.Fprintf(output, "socks: %s\n", address)
		},
		OnTraffic: func(snapshot nativeapp.TrafficSnapshot) {
			fmt.Fprintf(output, "traffic: upload=%s download=%s active=%d total=%d\n",
				formatTotalBytes(snapshot.UploadBytes), formatTotalBytes(snapshot.DownloadBytes),
				snapshot.ActiveConnections, snapshot.TotalConnections)
		},
		OnAccessEvidence: func(available bool) {
			fmt.Fprintf(output, "access: available=%t\n", available)
		},
	}
}
