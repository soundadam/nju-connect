package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

func TestBackgroundHandoffRoundTripAndValidation(t *testing.T) {
	token := make(sessiontoken.NativeGatewayToken, sessiontoken.NativeGatewayTokenSize)
	for index := range token {
		token[index] = byte(index)
	}
	handoff := backgroundHandoff{
		Settings: config.Config{
			Server:            "vpn.example.edu",
			Username:          "student",
			SOCKSListen:       "127.0.0.1:1081",
			NativeTLSInsecure: true,
		},
		Plan: core.DataplanePlan{
			Mode:               core.DataplaneL3VPN,
			LocalAgentRequired: true,
			BoundaryReady:      true,
		},
		Token:         token,
		NativeProfile: runtime.ProfileCommunityUTLSCompat,
		StatusPath:    runtimeStatusPath(t.TempDir()),
	}
	var wire bytes.Buffer
	if err := writeBackgroundHandoff(&wire, &handoff); err != nil {
		t.Fatal(err)
	}
	decoded, err := readBackgroundHandoff(&wire)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(decoded.Token)
	if !bytes.Equal(decoded.Token, token) || decoded.Settings.Server != handoff.Settings.Server || decoded.Plan.Mode != core.DataplaneL3VPN {
		t.Fatalf("decoded handoff lost required fields")
	}

	if _, err := readBackgroundHandoff(strings.NewReader(`{"token":"secret"}`)); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid handoff error = %v", err)
	}
}

func TestOpenPrivateBackgroundLogRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "runtime.log")
	file, err := openPrivateBackgroundLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("background log mode=%v", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateBackgroundLog(path); err == nil {
		t.Fatal("permissive background log was accepted")
	}
}
