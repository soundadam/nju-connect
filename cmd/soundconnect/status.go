package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
)

func runStatus(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect status", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	watch := flags.Bool("watch", false, "stream JSON status once per second")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	if *watch && !*asJSON {
		return app.Usagef("status --watch requires --json")
	}
	for {
		snapshot, err := app.Status(deps)
		if err != nil {
			return err
		}
		if err := writeRuntimeStatus(stdout, snapshot, *asJSON); err != nil {
			return fmt.Errorf("encode runtime status: %w", err)
		}
		if !*watch {
			if !snapshot.Running {
				return exitCode(1)
			}
			return nil
		}
		time.Sleep(time.Second)
	}
}

func writeRuntimeStatus(stdout io.Writer, snapshot runtimecontrol.Snapshot, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(snapshot)
	}
	fmt.Fprintf(stdout, "running: %t\nstate: %s\n", snapshot.Running, snapshot.State)
	if !snapshot.Running {
		return nil
	}
	fmt.Fprintf(stdout, "profile: %s\n", snapshot.Profile)
	if snapshot.StartedAt != nil {
		fmt.Fprintf(stdout, "started_at: %s\n", snapshot.StartedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(stdout, "socks_listen: %s\naccess_evidence: %s\n", snapshot.SOCKSListen, snapshot.AccessEvidence)
	if snapshot.LastCommandFailure != "" {
		fmt.Fprintf(stdout, "last_command_failure: %s\n", snapshot.LastCommandFailure)
	}
	if snapshot.LastDataFailure != "" {
		fmt.Fprintf(stdout, "last_data_failure: %s\n", snapshot.LastDataFailure)
	}
	if snapshot.Traffic != nil {
		fmt.Fprintf(stdout, "traffic: upload=%s download=%s active=%d total=%d\n",
			formatTotalBytes(snapshot.Traffic.UploadBytes), formatTotalBytes(snapshot.Traffic.DownloadBytes),
			snapshot.Traffic.ActiveConnections, snapshot.Traffic.TotalConnections)
	}
	return nil
}

func formatTotalBytes(bytes uint64) string {
	return fmt.Sprintf("%.1f KB", float64(bytes)/1_000)
}

func runDisconnect(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect disconnect", flag.ContinueOnError)
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	stopping, err := app.Disconnect(withInteraction(deps, app.LineOptions{}, stderr))
	if err != nil {
		return err
	}
	if stopping {
		fmt.Fprintln(stdout, "stopping: true")
	} else {
		fmt.Fprintln(stdout, "running: false")
	}
	return nil
}
