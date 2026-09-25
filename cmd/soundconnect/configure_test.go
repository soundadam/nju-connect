package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
)

func TestConfigureSwitchesToATrustWithoutTouchingSecrets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
	if err := config.Replace(paths.Config, config.Config{
		Backend:       backend.EasyConnect,
		Server:        config.DefaultServer,
		Username:      "123456789",
		SOCKSListen:   config.DefaultSOCKSListen,
		UpstreamProxy: "socks5://127.0.0.1:9050",
	}); err != nil {
		t.Fatal(err)
	}

	previousPaths := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previousPaths })

	var stdout, stderr bytes.Buffer
	if code := runConfigure([]string{"--backend", "atrust"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runConfigure() = %d, stderr = %q", code, stderr.String())
	}
	got, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendName() != backend.ATrust || got.Server != config.DefaultATrustServer ||
		got.Username != "123456789" || got.SOCKSListen != config.DefaultSOCKSListen ||
		got.AuthType != atrustPasswordAuthType || got.LoginDomain != "" ||
		got.UpstreamProxy != "socks5://127.0.0.1:9050" {
		t.Fatalf("configured profile = %#v", got)
	}
	if !strings.Contains(stdout.String(), "socks_listen: "+config.DefaultSOCKSListen) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestConfigureSwitchesBackToEasyConnectAndClearsATrustFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
	if err := config.Replace(paths.Config, config.Config{
		Backend:     backend.ATrust,
		Server:      config.DefaultATrustServer,
		Username:    "123456789",
		SOCKSListen: config.DefaultSOCKSListen,
		AuthType:    atrustPasswordAuthType,
		LoginDomain: "openldap13924",
	}); err != nil {
		t.Fatal(err)
	}

	previousPaths := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previousPaths })

	var stdout, stderr bytes.Buffer
	if code := runConfigure([]string{"--backend", "easyconnect"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runConfigure() = %d, stderr = %q", code, stderr.String())
	}
	got, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendName() != backend.EasyConnect || got.Server != config.DefaultServer ||
		got.SOCKSListen != config.DefaultSOCKSListen || got.AuthType != "" || got.LoginDomain != "" {
		t.Fatalf("configured profile = %#v", got)
	}
}

func TestConfigurePreservesTheSharedListenerAcrossBackendSwitches(t *testing.T) {
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{
		Root:       root,
		Config:     filepath.Join(root, "config.toml"),
		Credential: filepath.Join(root, "credential"),
	}
	const configuredListener = "127.0.0.1:19081"
	if err := config.Replace(paths.Config, config.Config{
		Backend:     backend.EasyConnect,
		Server:      config.DefaultServer,
		Username:    "123456789",
		SOCKSListen: configuredListener,
	}); err != nil {
		t.Fatal(err)
	}

	previousPaths := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previousPaths })

	var stdout, stderr bytes.Buffer
	if code := runConfigure([]string{"--backend", "atrust"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runConfigure() = %d, stderr = %q", code, stderr.String())
	}
	got, err := config.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got.SOCKSListen != configuredListener {
		t.Fatalf("SOCKSListen = %q, want shared setting %q", got.SOCKSListen, configuredListener)
	}
}

func TestConfigureRefusesLiveRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "soundconnect")
	paths := config.Paths{Root: root, Config: filepath.Join(root, "config.toml")}
	if err := config.Replace(paths.Config, config.Config{
		Backend:     backend.EasyConnect,
		Server:      config.DefaultServer,
		Username:    "student",
		SOCKSListen: config.DefaultSOCKSListen,
	}); err != nil {
		t.Fatal(err)
	}

	tracker := newRuntimeStatusTracker("community-utls")
	server, err := runtimecontrol.Serve(runtimecontrol.Path(root), tracker.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	previousPaths := resolveDefaultPaths
	resolveDefaultPaths = func() (config.Paths, error) { return paths, nil }
	t.Cleanup(func() { resolveDefaultPaths = previousPaths })

	var stdout, stderr bytes.Buffer
	if code := runConfigure([]string{"--backend", "atrust"}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "another native runtime is already active") {
		t.Fatalf("runConfigure() = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
