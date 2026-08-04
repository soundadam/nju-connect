package speedtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAutoRoutePrefersDirectEvenWhenRuntimeIsConnected(t *testing.T) {
	runtimeCalls := 0
	service := Service{
		Probe: func(_ context.Context, route Route, _ string) error {
			if route != RouteDirect {
				t.Fatalf("route = %s", route)
			}
			return nil
		},
		RuntimeStatus: func() (RuntimeState, error) {
			runtimeCalls++
			return RuntimeState{Connected: true, SOCKSListen: "127.0.0.1:1081"}, nil
		},
	}
	route, socks, err := service.selectRoute(context.Background(), RouteAuto, nil)
	if err != nil || route != RouteDirect || socks != "" || runtimeCalls != 0 {
		t.Fatalf("route=%s socks=%q runtimeCalls=%d err=%v", route, socks, runtimeCalls, err)
	}
}

func TestAutoRouteFallsBackOnlyToConnectedSoundConnect(t *testing.T) {
	var probes []Route
	service := Service{
		Probe: func(_ context.Context, route Route, socks string) error {
			probes = append(probes, route)
			if route == RouteDirect {
				return errors.New("unreachable")
			}
			if socks != "127.0.0.1:1081" {
				t.Fatalf("socks = %q", socks)
			}
			return nil
		},
		RuntimeStatus: func() (RuntimeState, error) {
			return RuntimeState{Connected: true, SOCKSListen: "127.0.0.1:1081"}, nil
		},
	}
	route, socks, err := service.selectRoute(context.Background(), RouteAuto, nil)
	if err != nil || route != RouteSoundConnect || socks != "127.0.0.1:1081" {
		t.Fatalf("route=%s socks=%q err=%v", route, socks, err)
	}
	if !reflect.DeepEqual(probes, []Route{RouteDirect, RouteSoundConnect}) {
		t.Fatalf("probes = %v", probes)
	}
}

func TestAutoRouteRequiresSoundConnectWhenDirectFails(t *testing.T) {
	service := Service{
		Probe:         func(context.Context, Route, string) error { return errors.New("unreachable") },
		RuntimeStatus: func() (RuntimeState, error) { return RuntimeState{}, nil },
	}
	_, _, err := service.selectRoute(context.Background(), RouteAuto, nil)
	if !errors.Is(err, ErrSoundConnectRequired) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHelperUsesPinnedArgumentsAndExplicitProxy(t *testing.T) {
	helper, argsPath := fakeHelper(t)
	result, err := runHelper(context.Background(), helperRunOptions{
		Path: helper, Route: RouteSoundConnect, SOCKSListen: "127.0.0.1:1081",
		Timeout: time.Second, VersionTimeout: time.Second, StartedAt: time.Unix(100, 0),
		Now: func() time.Time { return time.Unix(120, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSuccess || result.Route != RouteSoundConnect || result.DownloadMbps == nil || *result.DownloadMbps != 50 {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(data))
	for _, pair := range [][]string{
		{"--duration", "10"}, {"--concurrent", "3"}, {"--telemetry-level", "disabled"},
		{"--proxy", "socks5h://127.0.0.1:1081"},
	} {
		if !containsPair(args, pair[0], pair[1]) {
			t.Fatalf("args %v lack %v", args, pair)
		}
	}
	for _, value := range []string{"--json", "--progress-json", "--ipv4", "--no-icmp"} {
		if !contains(args, value) {
			t.Fatalf("args %v lack %s", args, value)
		}
	}
	if contains(args, "--share") {
		t.Fatalf("unsafe args = %v", args)
	}
}

func TestRunHelperDirectDoesNotPassProxy(t *testing.T) {
	helper, argsPath := fakeHelper(t)
	_, err := runHelper(context.Background(), helperRunOptions{
		Path: helper, Route: RouteDirect, Timeout: time.Second, VersionTimeout: time.Second,
		StartedAt: time.Unix(100, 0), Now: func() time.Time { return time.Unix(120, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(argsPath)
	if strings.Contains(string(data), "--proxy") {
		t.Fatalf("direct args = %s", data)
	}
}

func TestRunHelperPreservesCancellationAsResult(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "librespeed-cli")
	script := fmt.Sprintf(`#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf 'librespeed-cli %s (built on test)\n'
  exit 0
fi
cat >/dev/null
exec sleep 10
`, HelperVersion)
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := runHelper(ctx, helperRunOptions{
		Path: helper, Route: RouteDirect, Timeout: time.Second, VersionTimeout: time.Second,
		StartedAt: time.Unix(100, 0), Now: func() time.Time { return time.Unix(101, 0) },
	})
	if err == nil {
		if result.Status != StatusCancelled || result.ExitCode() != 130 {
			t.Fatalf("result = %#v", result)
		}
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestClassifyHelperFailureUsesStableSanitizedCodes(t *testing.T) {
	for input, want := range map[string]string{
		"Failed to get download speed: secret raw detail": "download_failure",
		"upload request failed":                           "upload_failure",
		"dial tcp private.example: reset":                 "connect_failure",
		"unexpected exit":                                 "helper_failed",
	} {
		failure := classifyHelperFailure(input)
		if failure.Code != want || strings.Contains(failure.Message, "private.example") || strings.Contains(failure.Message, "secret") {
			t.Fatalf("input=%q failure=%#v", input, failure)
		}
	}
}

func TestStoreAtomicallyKeepsOnlyLastResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "last-v1.json")
	store := Store{Path: path}
	first := successfulResult(RouteDirect, 10)
	second := successfulResult(RouteSoundConnect, 20)
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Route != RouteSoundConnect || loaded.DownloadMbps == nil || *loaded.DownloadMbps != 20 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestComponentInstallVerifiesAndPublishesAtomically(t *testing.T) {
	payload := []byte("synthetic executable payload")
	digest := sha256.Sum256(payload)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	manager := ComponentManager{
		Root: filepath.Join(t.TempDir(), "components"), Client: server.Client(),
		Asset: ComponentAsset{
			Version: "test", HelperVersion: HelperVersion, OS: runtime.GOOS, Architecture: runtime.GOARCH,
			URL: server.URL, Size: int64(len(payload)), SHA256: hex.EncodeToString(digest[:]),
		},
	}
	var progress []float64
	if err := manager.Install(context.Background(), func(event Event) {
		if event.Progress != nil {
			progress = append(progress, *event.Progress)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 1 {
		t.Fatalf("progress = %v", progress)
	}
	info, _ := os.Stat(manager.Path())
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestEmbeddedComponentCatalogIsCompleteForSupportedArchitectures(t *testing.T) {
	var catalog struct {
		ComponentVersion string                    `json:"component_version"`
		HelperVersion    string                    `json:"helper_version"`
		Assets           map[string]ComponentAsset `json:"assets"`
	}
	if err := json.Unmarshal(componentCatalogJSON, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, architecture := range []string{"arm64", "amd64"} {
		asset, ok := catalog.Assets[architecture]
		if !ok || catalog.ComponentVersion == "" || catalog.HelperVersion != HelperVersion || asset.Size <= 0 || len(asset.SHA256) != 64 || !strings.HasPrefix(asset.URL, "https://") {
			t.Fatalf("catalog asset %s = %#v", architecture, asset)
		}
	}
}

func TestComponentInstallRejectsWrongChecksum(t *testing.T) {
	payload := []byte("payload")
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	manager := ComponentManager{
		Root: filepath.Join(t.TempDir(), "components"), Client: server.Client(),
		Asset: ComponentAsset{
			Version: "test", HelperVersion: HelperVersion, OS: runtime.GOOS, Architecture: runtime.GOARCH,
			URL: server.URL, Size: int64(len(payload)), SHA256: strings.Repeat("a", 64),
		},
	}
	if err := manager.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(manager.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("component remains: %v", err)
	}
}

func fakeHelper(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "librespeed-cli")
	argsPath := filepath.Join(root, "args")
	script := fmt.Sprintf(`#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf 'librespeed-cli %s (built on test)\n'
  exit 0
fi
printf '%%s ' "$@" > %q
cat >/dev/null
printf '%%s\n' '{"type":"progress","test":"download","elapsed_ms":1000,"bytes":6250000,"mbps":50}' >&2
printf '%%s\n' '[{"server":{"name":"NJU Campus IPv4","url":"http://speed.nju.edu.cn"},"ping":6,"jitter":1,"upload":10,"download":50}]'
`, HelperVersion, argsPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, argsPath
}

func successfulResult(route Route, download float64) Result {
	upload, ping, jitter := 5.0, 6.0, 1.0
	return Result{
		SchemaVersion: SchemaVersion, Status: StatusSuccess,
		StartedAt: time.Unix(100, 0), EndedAt: time.Unix(120, 0),
		Target: TargetHost, Family: "ipv4", Route: route, Server: TargetHost,
		PingMS: &ping, JitterMS: &jitter, DownloadMbps: &download, UploadMbps: &upload,
		HelperVersion: HelperVersion,
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsPair(values []string, first, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}
