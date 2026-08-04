package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/soundadam/soundconnect/internal/speedtest"
	"golang.org/x/term"
)

var (
	speedtestStdin      io.Reader = os.Stdin
	speedtestIsTerminal           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	speedtestAsset                = speedtest.DefaultComponentAsset
	speedtestHTTPClient           = func() *http.Client { return nil }
	speedtestProbe                = speedtest.ProbeReachability
)

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func runSpeedtest(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) > 0 {
		switch arguments[0] {
		case "component":
			return runSpeedtestComponent(arguments[1:], stdout, stderr)
		case "last":
			return runSpeedtestLast(arguments[1:], stdout, stderr)
		case "campus":
			arguments = arguments[1:]
		}
	}
	flags := flag.NewFlagSet("speedtest campus", flag.ContinueOnError)
	flags.SetOutput(stderr)
	routeValue := flags.String("route", string(speedtest.RouteAuto), "auto, direct, or soundconnect")
	asJSON := flags.Bool("json", false, "print one JSON result")
	jsonEvents := flags.Bool("json-events", false, "emit versioned NDJSON events")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 || (*asJSON && *jsonEvents) {
		fmt.Fprintln(stderr, "speedtest campus accepts no positional arguments and JSON modes are mutually exclusive")
		return 2
	}
	route, err := speedtest.ParseRoute(*routeValue)
	if err != nil {
		return writeSpeedtestError(*asJSON, *jsonEvents, stdout, stderr, "invalid_arguments", err, 1)
	}
	paths, err := commandPaths()
	if err != nil {
		return writeSpeedtestError(*asJSON, *jsonEvents, stdout, stderr, "local_state", err, 1)
	}
	manager := speedtest.ComponentManager{
		Root: speedtest.ComponentRoot(paths.Root), Asset: speedtestAsset(), Client: speedtestHTTPClient(),
	}
	if err := manager.Validate(); err != nil {
		if *asJSON || *jsonEvents || !speedtestIsTerminal() {
			return writeSpeedtestError(*asJSON, *jsonEvents, stdout, stderr, "component_missing", err, 1)
		}
		status := manager.Status()
		if status.DownloadReady {
			fmt.Fprintf(stderr, "Campus speed testing requires component %s (%d bytes). Download now? [y/N] ", status.HelperVersion, status.DownloadSize)
		} else {
			fmt.Fprintf(stderr, "Campus speed-test component %s is not installed and has not been published.\n", status.HelperVersion)
			return 1
		}
		answer, readErr := bufio.NewReader(speedtestStdin).ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			fmt.Fprintf(stderr, "read component confirmation: %v\n", readErr)
			return 1
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
			fmt.Fprintln(stderr, "Campus speed-test component was not installed.")
			return 1
		}
		if err := manager.Install(context.Background(), plainSpeedtestSink(stderr)); err != nil {
			fmt.Fprintf(stderr, "install campus speed-test component: %v\n", err)
			return 1
		}
	}
	sink := plainSpeedtestSink(stderr)
	if *jsonEvents {
		sink = jsonSpeedtestSink(stdout)
	} else if *asJSON {
		sink = nil
	}
	service := speedtest.Service{
		HelperPath: manager.Path(), Store: speedtest.Store{Path: speedtest.LastResultPath(paths.Root)},
		Probe: speedtestProbe,
		RuntimeStatus: func() (speedtest.RuntimeState, error) {
			snapshot, err := queryRuntimeStatus(runtimeStatusPath(paths.Root))
			if err != nil {
				return speedtest.RuntimeState{}, err
			}
			return speedtest.RuntimeState{Connected: snapshot.State == "connected", SOCKSListen: snapshot.SOCKSListen}, nil
		},
	}
	ctx, cancel := signalContext()
	defer cancel()
	result, err := service.Run(ctx, route, sink)
	if err != nil {
		code := "speedtest_unavailable"
		exitCode := 1
		if errors.Is(err, speedtest.ErrSoundConnectRequired) {
			code = "soundconnect_required"
		} else if errors.Is(err, context.Canceled) {
			code = "cancelled"
			exitCode = 130
		}
		return writeSpeedtestError(*asJSON, *jsonEvents, stdout, stderr, code, err, exitCode)
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintf(stderr, "encode speed-test result: %v\n", err)
			return 1
		}
	} else if !*jsonEvents {
		renderSpeedtestResult(stdout, result)
	}
	return result.ExitCode()
}

func runSpeedtestComponent(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		fmt.Fprintln(stderr, "speedtest component requires status or install")
		return 2
	}
	command := arguments[0]
	flags := flag.NewFlagSet("speedtest component "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	yes := flags.Bool("yes", false, "install without a terminal prompt")
	jsonEvents := flags.Bool("json-events", false, "emit versioned NDJSON events")
	if err := flags.Parse(arguments[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || (*asJSON && *jsonEvents) {
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		return writeSpeedtestError(*asJSON, *jsonEvents, stdout, stderr, "local_state", err, 1)
	}
	manager := speedtest.ComponentManager{
		Root: speedtest.ComponentRoot(paths.Root), Asset: speedtestAsset(), Client: speedtestHTTPClient(),
	}
	switch command {
	case "status":
		if *yes || *jsonEvents {
			return 2
		}
		status := manager.Status()
		if *asJSON {
			_ = json.NewEncoder(stdout).Encode(status)
		} else {
			fmt.Fprintf(stdout, "installed: %t\nversion: %s\nhelper_version: %s\narchitecture: %s\ndownload_ready: %t\n",
				status.Installed, status.Version, status.HelperVersion, status.Architecture, status.DownloadReady)
		}
		if !status.Installed {
			return 1
		}
		return 0
	case "install":
		if *asJSON {
			return 2
		}
		if !*yes {
			if !speedtestIsTerminal() {
				return writeSpeedtestError(false, *jsonEvents, stdout, stderr, "interaction_required", errors.New("component installation requires --yes outside a terminal"), 1)
			}
			fmt.Fprint(stderr, "Download and install the campus speed-test component? [y/N] ")
			answer, _ := bufio.NewReader(speedtestStdin).ReadString('\n')
			if value := strings.ToLower(strings.TrimSpace(answer)); value != "y" && value != "yes" {
				return 1
			}
		}
		sink := plainSpeedtestSink(stderr)
		if *jsonEvents {
			sink = jsonSpeedtestSink(stdout)
		}
		if err := manager.Install(context.Background(), sink); err != nil {
			return writeSpeedtestError(false, *jsonEvents, stdout, stderr, "component_install_failed", err, 1)
		}
		if !*jsonEvents {
			fmt.Fprintln(stdout, "Campus speed-test component installed.")
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown speedtest component command %q\n", command)
		return 2
	}
}

func runSpeedtestLast(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("speedtest last", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 {
		return 2
	}
	paths, err := commandPaths()
	if err != nil {
		return 1
	}
	result, err := (speedtest.Store{Path: speedtest.LastResultPath(paths.Root)}).Load()
	if err != nil {
		return writeSpeedtestError(*asJSON, false, stdout, stderr, "no_speedtest_result", err, 1)
	}
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(result)
	} else {
		renderSpeedtestResult(stdout, result)
	}
	return 0
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
				fmt.Fprintf(output, "\rDownloading campus speed-test component: %.0f%%", *event.Progress*100)
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

func writeSpeedtestError(asJSON, events bool, stdout, stderr io.Writer, code string, err error, exitCode int) int {
	if asJSON || events {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"schema_version": speedtest.SchemaVersion, "type": "error",
			"error": map[string]string{"code": code, "message": err.Error()},
		})
	} else {
		fmt.Fprintf(stderr, "soundconnect speedtest: %v\n", err)
	}
	return exitCode
}
