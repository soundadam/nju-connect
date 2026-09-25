//go:build linux || darwin

package main

import (
	"regexp"
	"testing"

	"github.com/soundadam/soundconnect/internal/app"
	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/runtime"
)

// The tests in this file freeze the externally visible CLI surface described
// in docs/cli-contract.md: usage text, every command's help, exit codes, the
// stdout/stderr split, and the machine-readable output the macOS app parses.

func TestContractUsageAndDispatch(t *testing.T) {
	harness := newCLIHarness(t)
	for _, testCase := range []struct {
		name string
		args []string
		code int
	}{
		{"usage_help", []string{"help"}, 0},
		{"usage_long_help", []string{"--help"}, 0},
		{"usage_short_help", []string{"-h"}, 0},
		{"usage_unknown_command", []string{"bogus"}, 2},
		{"version", []string{"version"}, 0},
		{"native_runtime_rejects_arguments", []string{"_native-runtime", "extra"}, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness.golden(testCase.name, harness.run(testCase.args...).expect(t, testCase.code))
		})
	}
}

func TestContractCommandHelp(t *testing.T) {
	harness := newCLIHarness(t)
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"setup", []string{"setup", "-h"}},
		{"configure", []string{"configure", "-h"}},
		{"backends", []string{"backends", "-h"}},
		{"auth_info", []string{"auth-info", "-h"}},
		{"migrate", []string{"migrate", "-h"}},
		{"doctor", []string{"doctor", "-h"}},
		{"connect", []string{"connect", "-h"}},
		{"disconnect", []string{"disconnect", "-h"}},
		{"logout", []string{"logout", "-h"}},
		{"dry_run", []string{"dry-run", "-h"}},
		{"status", []string{"status", "-h"}},
		{"speedtest", []string{"speedtest", "-h"}},
		{"speedtest_campus", []string{"speedtest", "campus", "-h"}},
		{"speedtest_probe", []string{"speedtest", "probe", "-h"}},
		{"speedtest_last", []string{"speedtest", "last", "-h"}},
		{"speedtest_component_status", []string{"speedtest", "component", "status", "-h"}},
		{"speedtest_component_install", []string{"speedtest", "component", "install", "-h"}},
		{"status_long_help", []string{"status", "--help"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness.golden("help_"+testCase.name, harness.run(testCase.args...).expect(t, 0))
		})
	}
}

func TestContractUsageErrors(t *testing.T) {
	harness := newCLIHarness(t)
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{"unknown_flag", []string{"status", "--bogus"}},
		{"positional_argument", []string{"backends", "extra"}},
		{"setup_positional_argument", []string{"setup", "extra"}},
		{"setup_atrust_flags_on_easyconnect", []string{"setup", "--auth-type", "auth/psw"}},
		{"setup_unknown_backend", []string{"setup", "--backend", "openvpn"}},
		{"configure_unknown_backend", []string{"configure", "--backend", "openvpn"}},
		{"status_watch_without_json", []string{"status", "--watch"}},
		{"auth_info_easyconnect", []string{"auth-info", "--backend", "easyconnect"}},
		{"speedtest_unknown_subcommand", []string{"speedtest", "bogus"}},
		{"speedtest_json_and_events", []string{"speedtest", "--json", "--json-events"}},
		{"speedtest_component_missing_subcommand", []string{"speedtest", "component"}},
		{"speedtest_component_unknown_subcommand", []string{"speedtest", "component", "bogus"}},
		{"speedtest_component_status_events", []string{"speedtest", "component", "status", "--json-events"}},
		{"speedtest_component_install_json", []string{"speedtest", "component", "install", "--json"}},
		{"connect_positional_argument", []string{"connect", "extra"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness.golden("usage_error_"+testCase.name, harness.run(testCase.args...).expect(t, 2))
		})
	}
}

// Go's flag package accepts single-dash long flags today. Phase 2 (Cobra)
// deliberately drops them; this golden records the behaviour being removed.
func TestContractSingleDashLongFlagIsAcceptedBeforeCobra(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("single_dash_long_flag", harness.run("backends", "-json").expect(t, 0))
}

func TestContractBackends(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("backends_text", harness.run("backends").expect(t, 0))
	result := harness.run("backends", "--json").expect(t, 0)
	harness.golden("backends_json", result)
	contractFixture(t, "backends.json", result.stdout)
}

func TestContractDoctor(t *testing.T) {
	harness := newCLIHarness(t)

	missing := harness.run("doctor", "--json").expect(t, 1)
	harness.golden("doctor_missing_json", missing)
	contractFixture(t, "doctor_missing.json", missing.stdout)
	harness.golden("doctor_missing_text", harness.run("doctor").expect(t, 1))

	harness.writeConfig(config.Config{Server: "vpn.example.edu", Username: "student", SOCKSListen: config.DefaultSOCKSListen})
	harness.golden("doctor_credential_missing_text", harness.run("doctor").expect(t, 1))

	harness.writeSecret(harness.paths.Credential, "synthetic-password")
	ready := harness.run("doctor", "--json").expect(t, 0)
	harness.golden("doctor_ready_json", ready)
	contractFixture(t, "doctor_ready.json", ready.stdout)
	harness.golden("doctor_ready_text", harness.run("doctor").expect(t, 0))

	harness.writeConfig(config.Config{
		Backend: backend.ATrust, Server: config.DefaultATrustServer, SOCKSListen: config.DefaultSOCKSListen,
		AuthType: app.ATrustOAuthAuthType,
	})
	harness.golden("doctor_atrust_oauth_text", harness.run("doctor").expect(t, 0))
}

func TestContractAuthInfo(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("auth_info_text", harness.run("auth-info").expect(t, 0))
	harness.golden("auth_info_json", harness.run("auth-info", "--json").expect(t, 0))
}

func TestContractStatus(t *testing.T) {
	harness := newCLIHarness(t)

	stopped := harness.run("status", "--json").expect(t, 1)
	harness.golden("status_stopped_json", stopped)
	contractFixture(t, "status_stopped.json", stopped.stdout)
	harness.golden("status_stopped_text", harness.run("status").expect(t, 1))

	harness.serveStatus(fixedRunningSnapshot(runtime.ProfileCommunityUTLSCompat))
	running := harness.run("status", "--json").expect(t, 0)
	harness.golden("status_running_json", running)
	contractFixture(t, "status_running.json", running.stdout)
	harness.golden("status_running_text", harness.run("status").expect(t, 0))
}

func TestContractStatusATrustProfile(t *testing.T) {
	harness := newCLIHarness(t)
	harness.serveStatus(fixedRunningSnapshot(runtime.ProfileATrustTCP))
	running := harness.run("status", "--json").expect(t, 0)
	contractFixture(t, "status_running_atrust.json", running.stdout)
}

func TestContractDisconnect(t *testing.T) {
	harness := newCLIHarness(t)
	harness.golden("disconnect_not_running", harness.run("disconnect").expect(t, 0))

	control := harness.serveStatus(fixedRunningSnapshot(runtime.ProfileCommunityUTLSCompat))
	harness.golden("disconnect_running", harness.run("disconnect").expect(t, 0))
	if control.stopCount() != 1 {
		t.Fatalf("runtime stop requests = %d", control.stopCount())
	}
}

func TestContractConfigure(t *testing.T) {
	harness := newCLIHarness(t)
	// aTrust defaults to shared-password authentication, which needs an account.
	harness.golden("configure_atrust_without_account", harness.run("configure", "--backend", "atrust").expect(t, 2))
	harness.golden("configure_atrust_from_scratch",
		harness.run("configure", "--backend", "atrust", "--username", "student").expect(t, 0))
	harness.golden("configure_back_to_easyconnect", harness.run("configure", "--backend", "easyconnect").expect(t, 0))

	harness.serveStatus(fixedRunningSnapshot(runtime.ProfileCommunityUTLSCompat))
	harness.golden("configure_refuses_live_runtime", harness.run("configure", "--backend", "atrust").expect(t, 1))
}

var rfc3339Timestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// stableTimes replaces wall-clock timestamps with a fixed RFC 3339 value.
// The replacement keeps nanosecond digits because Go writes them whenever the
// clock has them, and the macOS app must decode that shape.
func stableTimes(value string) string {
	return rfc3339Timestamp.ReplaceAllString(value, "2026-09-01T08:00:00.123456789Z")
}

func stableJSONNumber(field string) func(string) string {
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(field) + `":[0-9.eE+-]+`)
	return func(value string) string {
		return pattern.ReplaceAllString(value, `"`+field+`":1.5`)
	}
}
