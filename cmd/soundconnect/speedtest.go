package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/speedtest"
)

type campusOptions struct {
	route      string
	asJSON     bool
	jsonEvents bool
}

func addCampusFlags(command *cobra.Command, options *campusOptions) {
	flags := command.Flags()
	flags.StringVar(&options.route, "route", string(speedtest.RouteAuto), "auto, direct, or soundconnect")
	flags.BoolVar(&options.asJSON, "json", false, "print one JSON result")
	flags.BoolVar(&options.jsonEvents, "json-events", false, "emit versioned NDJSON events")
}

// newSpeedtestCommand runs campus when given no subcommand, so it takes
// the campus flags itself.
func newSpeedtestCommand(deps app.Deps) *cobra.Command {
	var options campusOptions
	command := &cobra.Command{
		Use: "speedtest",
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) > 0 {
				return app.Usagef("unknown speedtest command %q\nspeedtest commands are campus (default), component, and last", arguments[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSpeedtestCampus(cmd, deps, options)
		},
	}
	addCampusFlags(command, &options)
	command.AddCommand(
		newSpeedtestCampusCommand(deps),
		newSpeedtestProbeCommand(deps),
		newSpeedtestLastCommand(deps),
		newSpeedtestComponentCommand(deps),
	)
	return command
}

func newSpeedtestCampusCommand(deps app.Deps) *cobra.Command {
	var options campusOptions
	command := &cobra.Command{
		Use: "campus",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSpeedtestCampus(cmd, deps, options)
		},
	}
	addCampusFlags(command, &options)
	return command
}

func runSpeedtestCampus(cmd *cobra.Command, deps app.Deps, options campusOptions) error {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	asJSON, jsonEvents := options.asJSON, options.jsonEvents
	if asJSON && jsonEvents {
		return app.Usagef("--json and --json-events are mutually exclusive")
	}
	route, err := speedtest.ParseRoute(options.route)
	if err != nil {
		return writeSpeedtestError(asJSON, jsonEvents, stdout, stderr, "invalid_arguments", err, 1)
	}
	deps = withInteraction(deps, app.LineOptions{}, stderr)
	campus, err := app.OpenSpeedtest(deps)
	if err != nil {
		return writeSpeedtestError(asJSON, jsonEvents, stdout, stderr, "local_state", err, 1)
	}
	manager := campus.Component
	if err := manager.Validate(); err != nil {
		if asJSON || jsonEvents || !deps.Interactive {
			return writeSpeedtestError(asJSON, jsonEvents, stdout, stderr, "component_missing", err, 1)
		}
		status := manager.Status()
		if !status.DownloadReady {
			fmt.Fprintln(stderr, "Campus speed testing requires the external librespeed-cli-soundconnect helper.")
			fmt.Fprintln(stderr, "On macOS install it with: brew install soundadam/local/librespeed-cli-soundconnect")
			return exitCode(1)
		}
		install, err := deps.Interaction.Confirm(cmd.Context(), fmt.Sprintf(
			"Campus speed testing requires the %s component (%d bytes). Download it now?", status.HelperVersion, status.DownloadSize), false)
		if err != nil {
			fmt.Fprintf(stderr, "read component confirmation: %v\n", err)
			return exitCode(1)
		}
		if !install {
			fmt.Fprintln(stderr, "Campus speed-test component was not installed.")
			return exitCode(1)
		}
		if err := manager.Install(cmd.Context(), plainSpeedtestSink(stderr)); err != nil {
			fmt.Fprintf(stderr, "install campus speed-test component: %v\n", err)
			return exitCode(1)
		}
	}
	sink := plainSpeedtestSink(stderr)
	if jsonEvents {
		sink = jsonSpeedtestSink(stdout)
	} else if asJSON {
		sink = nil
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer cancel()
	result, err := campus.Service.Run(ctx, route, sink)
	if err != nil {
		code := "speedtest_unavailable"
		status := 1
		if errors.Is(err, speedtest.ErrSoundConnectRequired) {
			code = "soundconnect_required"
		} else if errors.Is(err, context.Canceled) {
			code = "cancelled"
			status = 130
		}
		return writeSpeedtestError(asJSON, jsonEvents, stdout, stderr, code, err, status)
	}
	if asJSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "encode speed-test result: %v\n", err)
			return exitCode(1)
		}
	} else if !jsonEvents {
		renderSpeedtestResult(stdout, result)
	}
	if status := result.ExitCode(); status != 0 {
		return exitCode(status)
	}
	return nil
}

func newSpeedtestProbeCommand(deps app.Deps) *cobra.Command {
	var routeValue string
	var asJSON bool
	command := &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			route, err := speedtest.ParseRoute(routeValue)
			if err != nil {
				return writeSpeedtestError(asJSON, false, stdout, stderr, "invalid_arguments", err, 1)
			}
			campus, err := app.OpenSpeedtest(deps)
			if err != nil {
				return writeSpeedtestError(asJSON, false, stdout, stderr, "local_state", err, 1)
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer cancel()
			result, err := campus.Service.ProbeRoute(ctx, route)
			if err != nil {
				code := "speedtest_unavailable"
				if errors.Is(err, speedtest.ErrSoundConnectRequired) {
					code = "soundconnect_required"
				}
				return writeSpeedtestError(asJSON, false, stdout, stderr, code, err, 1)
			}
			if asJSON {
				if err := json.NewEncoder(stdout).Encode(result); err != nil {
					fmt.Fprintf(stderr, "encode speed-test probe: %v\n", err)
					return exitCode(1)
				}
			} else {
				fmt.Fprintf(stdout, "target: %s\nroute: %s\nlatency_ms: %.2f\n", result.Target, result.Route, result.LatencyMS)
			}
			return nil
		},
	}
	command.Flags().StringVar(&routeValue, "route", string(speedtest.RouteAuto), "auto, direct, or soundconnect")
	command.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return command
}

type componentOptions struct {
	asJSON     bool
	yes        bool
	jsonEvents bool
}

// addComponentFlags gives status and install the same flags, so a flag
// meant for the other one is a clear usage error rather than an unknown
// flag; each hides the flags it rejects.
func addComponentFlags(command *cobra.Command, options *componentOptions, hidden ...string) {
	flags := command.Flags()
	flags.BoolVar(&options.asJSON, "json", false, "print JSON")
	flags.BoolVar(&options.yes, "yes", false, "install without a terminal prompt")
	flags.BoolVar(&options.jsonEvents, "json-events", false, "emit versioned NDJSON events")
	for _, name := range hidden {
		_ = flags.MarkHidden(name)
	}
}

func newSpeedtestComponentCommand(deps app.Deps) *cobra.Command {
	command := &cobra.Command{
		Use: "component",
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) > 0 {
				return app.Usagef("unknown speedtest component command %q", arguments[0])
			}
			return nil
		},
		RunE: func(*cobra.Command, []string) error {
			return app.Usagef("speedtest component requires status or install")
		},
	}
	command.AddCommand(newSpeedtestComponentStatusCommand(deps), newSpeedtestComponentInstallCommand(deps))
	return command
}

func newSpeedtestComponentStatusCommand(deps app.Deps) *cobra.Command {
	var options componentOptions
	command := &cobra.Command{
		Use: "status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if options.asJSON && options.jsonEvents {
				return app.Usagef("--json and --json-events are mutually exclusive")
			}
			if options.yes || options.jsonEvents {
				return app.Usagef("speedtest component status supports only --json")
			}
			campus, err := app.OpenSpeedtest(withInteraction(deps, app.LineOptions{}, stderr))
			if err != nil {
				return writeSpeedtestError(options.asJSON, false, stdout, stderr, "local_state", err, 1)
			}
			status := campus.Component.Status()
			if options.asJSON {
				_ = json.NewEncoder(stdout).Encode(status)
			} else {
				fmt.Fprintf(stdout, "installed: %t\nversion: %s\nhelper_version: %s\narchitecture: %s\ndownload_ready: %t\n",
					status.Installed, status.Version, status.HelperVersion, status.Architecture, status.DownloadReady)
			}
			if !status.Installed {
				return exitCode(1)
			}
			return nil
		},
	}
	addComponentFlags(command, &options, "yes", "json-events")
	return command
}

func newSpeedtestComponentInstallCommand(deps app.Deps) *cobra.Command {
	var options componentOptions
	command := &cobra.Command{
		Use: "install",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if options.asJSON && options.jsonEvents {
				return app.Usagef("--json and --json-events are mutually exclusive")
			}
			if options.asJSON {
				return app.Usagef("speedtest component install streams progress; use --json-events instead of --json")
			}
			jsonEvents := options.jsonEvents
			deps := withInteraction(deps, app.LineOptions{}, stderr)
			campus, err := app.OpenSpeedtest(deps)
			if err != nil {
				return writeSpeedtestError(false, jsonEvents, stdout, stderr, "local_state", err, 1)
			}
			manager := campus.Component
			if !options.yes {
				if !deps.Interactive {
					return writeSpeedtestError(false, jsonEvents, stdout, stderr, "interaction_required", errors.New("component installation requires --yes outside a terminal"), 1)
				}
				install, err := deps.Interaction.Confirm(cmd.Context(), "Install the campus speed-test component?", false)
				if err != nil || !install {
					return exitCode(1)
				}
			}
			if manager.Status().InstallSource == "" {
				return writeSpeedtestError(false, jsonEvents, stdout, stderr, "component_install_failed",
					errors.New("install the external helper with: brew install soundadam/local/librespeed-cli-soundconnect"), 1)
			}
			sink := plainSpeedtestSink(stderr)
			if jsonEvents {
				sink = jsonSpeedtestSink(stdout)
			}
			if err := manager.Install(cmd.Context(), sink); err != nil {
				return writeSpeedtestError(false, jsonEvents, stdout, stderr, "component_install_failed", err, 1)
			}
			if !jsonEvents {
				fmt.Fprintln(stdout, "Campus speed-test component installed.")
			}
			return nil
		},
	}
	addComponentFlags(command, &options, "json")
	return command
}

func newSpeedtestLastCommand(deps app.Deps) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "last",
		RunE: func(cmd *cobra.Command, _ []string) error {
			stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
			campus, err := app.OpenSpeedtest(deps)
			if err != nil {
				return writeSpeedtestError(asJSON, false, stdout, stderr, "local_state", err, 1)
			}
			result, err := campus.Store.Load()
			if err != nil {
				return writeSpeedtestError(asJSON, false, stdout, stderr, "no_speedtest_result", err, 1)
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(result)
			} else {
				renderSpeedtestResult(stdout, result)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return command
}

func jsonSpeedtestSink(output io.Writer) speedtest.ProgressSink {
	encoder := json.NewEncoder(output)
	return func(event speedtest.Event) { _ = encoder.Encode(event) }
}

func plainSpeedtestSink(output io.Writer) speedtest.ProgressSink {
	return func(event speedtest.Event) {
		switch event.Type {
		case "component_progress":
			if event.Progress != nil {
				verb := "Downloading"
				if event.Phase == "installing" {
					verb = "Installing"
				}
				fmt.Fprintf(output, "\r%s campus speed-test component: %.0f%%", verb, *event.Progress*100)
				if event.Phase == "complete" {
					fmt.Fprintln(output)
				}
			}
		case "measurement_progress":
			if event.Mbps != nil {
				fmt.Fprintf(output, "\rCampus speed test %-8s %7.2f Mbps", event.Phase, *event.Mbps)
			}
		}
	}
}

func renderSpeedtestResult(output io.Writer, result speedtest.Result) {
	fmt.Fprintf(output, "\nstatus: %s\nroute: %s\ntarget: %s\n", result.Status, result.Route, result.Target)
	if result.DownloadMbps != nil {
		fmt.Fprintf(output, "download_mbps: %.2f\n", *result.DownloadMbps)
	}
	if result.UploadMbps != nil {
		fmt.Fprintf(output, "upload_mbps: %.2f\n", *result.UploadMbps)
	}
	if result.PingMS != nil {
		fmt.Fprintf(output, "ping_ms: %.2f\n", *result.PingMS)
	}
	if result.Failure != nil {
		fmt.Fprintf(output, "failure: %s/%s: %s\n", result.Failure.Stage, result.Failure.Code, result.Failure.Message)
	}
}

// writeSpeedtestError reports a failure as a JSON error object on stdout in
// the machine-readable modes and as a line on stderr otherwise.
func writeSpeedtestError(asJSON, events bool, stdout, stderr io.Writer, code string, err error, status int) error {
	if asJSON || events {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"schema_version": speedtest.SchemaVersion, "type": "error",
			"error": map[string]string{"code": code, "message": err.Error()},
		})
	} else {
		fmt.Fprintf(stderr, "soundconnect speedtest: %v\n", err)
	}
	return exitCode(status)
}
