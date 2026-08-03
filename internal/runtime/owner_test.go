package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type ownerRunner struct {
	reports chan componentEvent
	joined  chan struct{}
}

func (runner *ownerRunner) Run(ctx context.Context, report func(Component, bool)) error {
	defer close(runner.joined)
	for {
		select {
		case event := <-runner.reports:
			report(event.component, event.ready)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestOwnerWatchdogRenewsOnceAndJoinsAllWorkers(t *testing.T) {
	now := time.Unix(500, 0)
	timer := make(chan time.Time, 1)
	runners := make([]*ownerRunner, componentCount)
	configured := make([]Runner, componentCount)
	for index := range runners {
		runners[index] = &ownerRunner{reports: make(chan componentEvent, 2), joined: make(chan struct{})}
		configured[index] = runners[index]
	}
	var statesMu sync.Mutex
	var states []State
	owner, err := NewOwner(OwnerConfig{
		Runners: configured, Watchdog: 2 * time.Minute,
		Now:   func() time.Time { return now },
		After: func(context.Context, time.Duration) <-chan time.Time { return timer },
		OnState: func(state State) {
			statesMu.Lock()
			states = append(states, state)
			statesMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- owner.Run(context.Background()) }()
	for component := ComponentCommand; component < componentCount; component++ {
		runners[component].reports <- componentEvent{component: component, ready: true}
	}
	eventuallyState(t, &statesMu, &states, StateConnected)
	now = now.Add(time.Second)
	runners[ComponentCommand].reports <- componentEvent{component: ComponentCommand, ready: false}
	eventuallyState(t, &statesMu, &states, StateReconnecting)
	// A later partial failure does not replace the original watchdog.
	now = now.Add(time.Minute)
	runners[ComponentRX].reports <- componentEvent{component: ComponentRX, ready: false}
	timer <- time.Unix(500, 0).Add(2*time.Minute + time.Second)
	err = <-result
	var renewal *RenewalRequired
	if !errors.As(err, &renewal) || renewal.Reason != RenewalReconnectTimeout {
		t.Fatalf("owner error = %#v", err)
	}
	for index, runner := range runners {
		select {
		case <-runner.joined:
		case <-time.After(time.Second):
			t.Fatalf("runner %d was not joined", index)
		}
	}
}

func eventuallyState(t *testing.T, mu *sync.Mutex, states *[]State, want State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := false
		for _, state := range *states {
			found = found || state == want
		}
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("state %q was not observed", want)
}
