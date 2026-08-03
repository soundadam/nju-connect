package runtime

import (
	"errors"
	"testing"
	"time"
)

func TestReadinessRequiresAllComponentsAndKeepsOriginalWatchdogDeadline(t *testing.T) {
	start := time.Unix(100, 0)
	var readiness Readiness
	for component := ComponentCommand; component < ComponentSOCKS; component++ {
		if got := readiness.Mark(component, true, start); got != StateConnecting {
			t.Fatalf("component %d made state %q before all ready", component, got)
		}
	}
	if got := readiness.Mark(ComponentSOCKS, true, start); got != StateConnected {
		t.Fatalf("all ready state = %q, want connected", got)
	}
	if got := readiness.Mark(ComponentCommand, false, start.Add(time.Second)); got != StateReconnecting {
		t.Fatalf("command loss state = %q", got)
	}
	readiness.Mark(ComponentRX, false, start.Add(time.Minute))
	if since, ok := readiness.ReconnectingSince(); !ok || !since.Equal(start.Add(time.Second)) {
		t.Fatalf("reconnecting since = %v, %v", since, ok)
	}
	if err := readiness.Watchdog(start.Add(2*time.Minute), 2*time.Minute); err != nil {
		t.Fatalf("watchdog fired early: %v", err)
	}
	err := readiness.Watchdog(start.Add(2*time.Minute+time.Second), 2*time.Minute)
	if !errors.Is(err, ErrRenewalRequired) {
		t.Fatalf("watchdog error = %v", err)
	}
	if err := readiness.Watchdog(start.Add(3*time.Minute), 2*time.Minute); err != nil {
		t.Fatalf("watchdog issued renewal more than once: %v", err)
	}
}

func TestReadinessRecoveryCancelsReconnectWindow(t *testing.T) {
	now := time.Unix(200, 0)
	var readiness Readiness
	for component := ComponentCommand; component < componentCount; component++ {
		readiness.Mark(component, true, now)
	}
	readiness.Mark(ComponentTX, false, now.Add(time.Second))
	readiness.Mark(ComponentTX, true, now.Add(10*time.Second))
	if got := readiness.State(); got != StateConnected {
		t.Fatalf("state = %q", got)
	}
	if _, ok := readiness.ReconnectingSince(); ok {
		t.Fatal("reconnect window remains after full recovery")
	}
}
