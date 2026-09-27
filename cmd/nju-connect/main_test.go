package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/soundadam/nju-connect/internal/app"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d", code)
	}
	if got := stdout.String(); got != "nju-connect dev\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHelpPrintsUsageOnStdoutAndSucceeds(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) = %d", code)
	}
	if !strings.Contains(stdout.String(), "usage: nju-connect") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "observe") {
		t.Fatalf("usage advertises an unimplemented command: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSubcommandHelpRequestPrintsFlagsOnStdoutAndSucceeds(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"status", "-h"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(status -h) = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage of nju-connect status:") || !strings.Contains(stdout.String(), "-json") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUnimplementedObserveCommandIsNotDispatched(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"observe"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(observe) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "observe"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run(isolatedDeps(t), []string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown) = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDefaultAndConnectDispatchToRuntimeWhileDryRunStaysExplicit(t *testing.T) {
	paths, deps := useATrustTestState(t)
	core := useATrustTestCore(t, &deps)
	core.methods = nil
	writeATrustTestConfig(t, paths, app.ATrustPasswordAuthType, "")

	for _, arguments := range [][]string{nil, {"connect"}} {
		var stdout, stderr bytes.Buffer
		if code := run(deps, arguments, &stdout, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "select aTrust authentication") {
			t.Fatalf("run(%v) = %d, stderr = %q", arguments, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(deps, []string{"dry-run", "-h"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "nju-connect dry-run") {
		t.Fatalf("run(dry-run -h) = %d, stdout = %q", code, stdout.String())
	}
}

func TestNativeConnectCommandWasReplaced(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(isolatedDeps(t), []string{"native-connect"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(native-connect) = %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "native-connect"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestReleaseCommandsRejectWorktreeDevelopmentOverride(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(isolatedDeps(t), []string{"doctor", "--worktree", t.TempDir()}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(doctor --worktree) = %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag: --worktree") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpstreamDebugLogNeedsNJUConnectDebug(t *testing.T) {
	stderr := &bytes.Buffer{}
	for value, want := range map[string]bool{"": false, "0": false, "true": false, "1": true} {
		getenv := func(name string) string {
			if name == "NJU_CONNECT_DEBUG" {
				return value
			}
			return ""
		}
		if got := upstreamDebugLog(getenv, stderr); (got == stderr) != want || (got != nil) != want {
			t.Errorf("NJU_CONNECT_DEBUG=%q: debug log = %v, want enabled %v", value, got, want)
		}
	}
}
