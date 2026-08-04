package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/speedtest"
)

func TestSpeedtestJSONFailsClosedWithoutPublishedComponent(t *testing.T) {
	root := t.TempDir()
	installSpeedtestTestDependencies(t, root)
	speedtestIsTerminal = func() bool { return false }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"speedtest", "--json"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code = %d stderr=%q", code, stderr.String())
	}
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "error" || payload.Error.Code != "component_missing" {
		t.Fatalf("payload = %#v", payload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInteractiveSpeedtestDownloadsComponentAndRuns(t *testing.T) {
	root := t.TempDir()
	installSpeedtestTestDependencies(t, root)
	helper := []byte(`#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf 'librespeed-cli v1.0.13-soundconnect.1 (built on test)\n'
  exit 0
fi
cat >/dev/null
printf '%s\n' '{"type":"progress","test":"download","elapsed_ms":1000,"bytes":6250000,"mbps":50}' >&2
printf '%s\n' '[{"server":{"name":"NJU Campus IPv4","url":"http://speed.nju.edu.cn"},"ping":6,"jitter":1,"upload":10,"download":50}]'
`)
	digest := sha256.Sum256(helper)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(helper)
	}))
	defer server.Close()
	speedtestAsset = func() speedtest.ComponentAsset {
		return speedtest.ComponentAsset{
			Version: "test", HelperVersion: speedtest.HelperVersion,
			OS: runtime.GOOS, Architecture: runtime.GOARCH,
			URL: server.URL, Size: int64(len(helper)), SHA256: hex.EncodeToString(digest[:]),
		}
	}
	speedtestHTTPClient = func() *http.Client { return server.Client() }
	speedtestIsTerminal = func() bool { return true }
	speedtestStdin = strings.NewReader("yes\n")
	speedtestProbe = func(context.Context, speedtest.Route, string) error { return nil }

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"speedtest"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "download_mbps: 50.00") || !strings.Contains(stdout.String(), "route: direct") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	last, err := (speedtest.Store{Path: speedtest.LastResultPath(root)}).Load()
	if err != nil || last.Status != speedtest.StatusSuccess {
		t.Fatalf("last=%#v err=%v", last, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"speedtest", "--json-events"}, &stdout, &stderr); code != 0 {
		t.Fatalf("events code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "\x1b") || !strings.Contains(stdout.String(), `"type":"result"`) {
		t.Fatalf("events = %q", stdout.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("invalid NDJSON line %q: %v", line, err)
		}
	}
}

func TestSpeedtestLastJSONReadsSavedResult(t *testing.T) {
	root := t.TempDir()
	installSpeedtestTestDependencies(t, root)
	download, upload := 50.0, 10.0
	result := speedtest.Result{
		SchemaVersion: speedtest.SchemaVersion, Status: speedtest.StatusSuccess,
		StartedAt: parseTime(t, "2026-08-04T00:00:00Z"), EndedAt: parseTime(t, "2026-08-04T00:00:20Z"),
		Target: speedtest.TargetHost, Family: "ipv4", Route: speedtest.RouteDirect,
		Server: speedtest.TargetHost, DownloadMbps: &download, UploadMbps: &upload,
		HelperVersion: speedtest.HelperVersion,
	}
	if err := (speedtest.Store{Path: speedtest.LastResultPath(root)}).Save(result); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"speedtest", "last", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"download_mbps":50`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSpeedtestProbeDoesNotRequireMeasurementComponent(t *testing.T) {
	root := t.TempDir()
	installSpeedtestTestDependencies(t, root)
	speedtestProbe = func(_ context.Context, route speedtest.Route, socks string) error {
		if route != speedtest.RouteDirect || socks != "" {
			t.Fatalf("route=%s socks=%q", route, socks)
		}
		return nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"speedtest", "probe", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var result speedtest.ProbeResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != speedtest.SchemaVersion || result.Target != speedtest.TargetHost || result.Route != speedtest.RouteDirect {
		t.Fatalf("result = %#v", result)
	}
}

func installSpeedtestTestDependencies(t *testing.T, root string) {
	t.Helper()
	previousPaths := resolveDefaultPaths
	previousAsset := speedtestAsset
	previousBundledPath := speedtestBundledPath
	previousHTTP := speedtestHTTPClient
	previousTerminal := speedtestIsTerminal
	previousStdin := speedtestStdin
	previousProbe := speedtestProbe
	resolveDefaultPaths = func() (config.Paths, error) {
		return config.Paths{Root: root, Config: filepath.Join(root, "config.toml"), Credential: filepath.Join(root, "credential")}, nil
	}
	speedtestBundledPath = func(speedtest.ComponentAsset) string { return "" }
	t.Cleanup(func() {
		resolveDefaultPaths = previousPaths
		speedtestAsset = previousAsset
		speedtestBundledPath = previousBundledPath
		speedtestHTTPClient = previousHTTP
		speedtestIsTerminal = previousTerminal
		speedtestStdin = previousStdin
		speedtestProbe = previousProbe
	})
}

func parseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
