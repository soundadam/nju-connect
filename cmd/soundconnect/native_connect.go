package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/backend/easyconnect/session"
	"github.com/soundadam/soundconnect/internal/runtime"
)

func runConnect(deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return exitStatus(connect(ctx, deps, arguments, stdout, stderr), stderr)
}

func connect(ctx context.Context, deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect connect", flag.ContinueOnError)
	background := flags.Bool("background", false, "continue the native runtime as a detached process after authentication")
	verificationCodeStdin := flags.Bool("verification-code-stdin", false, "read the verification code from standard input without requiring a terminal")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	deps = withInteraction(deps, app.LineOptions{CodeFromStdin: *verificationCodeStdin}, stderr)
	result, err := app.Connect(ctx, deps, app.ConnectRequest{
		Background: *background,
		OfferSetup: !*verificationCodeStdin,
	}, connectEvents(stdout))
	if err != nil {
		return err
	}
	if *background {
		fmt.Fprintf(stdout, "background: pid=%d log=%s\n", result.BackgroundPID, result.LogPath)
	}
	return nil
}

// runBackgroundRuntime is the hidden command a background connect starts.
func runBackgroundRuntime(deps app.Deps, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) != 0 {
		return exitStatus(app.Usagef("background runtime accepts no arguments"), stderr)
	}
	handoff, ready, err := app.BackgroundFiles()
	if err != nil {
		return exitStatus(err, stderr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deps.Diagnostics = stderr
	code := exitStatus(app.RunBackground(ctx, deps, handoff, ready, connectEvents(stdout)), stderr)
	_ = ready.Close()
	return code
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
