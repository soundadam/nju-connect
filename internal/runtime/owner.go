package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Reporter func(Component, bool)

type Runner interface {
	Run(context.Context, func(Component, bool)) error
}

type RunnerFunc func(context.Context, func(Component, bool)) error

func (run RunnerFunc) Run(ctx context.Context, report func(Component, bool)) error {
	return run(ctx, report)
}

type OwnerConfig struct {
	Runners  []Runner
	Watchdog time.Duration
	Now      func() time.Time
	After    func(context.Context, time.Duration) <-chan time.Time
	OnState  func(State)
}

type Owner struct {
	config OwnerConfig
}

func NewOwner(config OwnerConfig) (*Owner, error) {
	if len(config.Runners) == 0 {
		return nil, errors.New("at least one runtime runner is required")
	}
	for _, runner := range config.Runners {
		if runner == nil {
			return nil, errors.New("runtime runner is nil")
		}
	}
	if config.Watchdog <= 0 {
		config.Watchdog = 2 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.After == nil {
		config.After = afterContext
	}
	return &Owner{config: config}, nil
}

type componentEvent struct {
	component Component
	ready     bool
}

func (owner *Owner) Run(ctx context.Context) error {
	session, cancel := context.WithCancel(ctx)
	events := make(chan componentEvent, componentCount*2)
	results := make(chan error, len(owner.config.Runners))
	var workers sync.WaitGroup
	report := func(component Component, ready bool) {
		select {
		case events <- componentEvent{component: component, ready: ready}:
		case <-session.Done():
		}
	}
	for _, runner := range owner.config.Runners {
		workers.Add(1)
		go func(runner Runner) {
			defer workers.Done()
			results <- runner.Run(session, report)
		}(runner)
	}

	readiness := Readiness{}
	lastState := StateConnecting
	if owner.config.OnState != nil {
		owner.config.OnState(lastState)
	}
	var watchdog <-chan time.Time
	var cancelWatchdog context.CancelFunc
	stopWatchdog := func() {
		if cancelWatchdog != nil {
			cancelWatchdog()
			cancelWatchdog = nil
		}
		watchdog = nil
	}
	finish := func(err error) error {
		stopWatchdog()
		cancel()
		workers.Wait()
		return err
	}
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return finish(ctx.Err())
		case event := <-events:
			state := readiness.Mark(event.component, event.ready, owner.config.Now())
			if state != lastState {
				lastState = state
				if owner.config.OnState != nil {
					owner.config.OnState(state)
				}
			}
			if state == StateReconnecting && watchdog == nil {
				watchdog, cancelWatchdog = startWatchdog(session, owner.config.Watchdog, owner.config.After)
			}
			if state == StateConnected {
				stopWatchdog()
			}
		case now := <-watchdog:
			stopWatchdog()
			if err := readiness.Watchdog(now, owner.config.Watchdog); err != nil {
				return finish(err)
			}
		case err := <-results:
			if err == nil {
				err = &TransportFailure{Code: FailureRuntimeStopped}
			}
			if errors.Is(err, ErrRenewalRequired) {
				return finish(err)
			}
			if session.Err() == nil {
				return finish(&TransportFailure{Code: FailureTransportUnavailable})
			}
		}
	}
}

func startWatchdog(parent context.Context, duration time.Duration, after func(context.Context, time.Duration) <-chan time.Time) (<-chan time.Time, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	return after(ctx, duration), cancel
}

func afterContext(ctx context.Context, duration time.Duration) <-chan time.Time {
	result := make(chan time.Time, 1)
	timer := time.NewTimer(duration)
	go func() {
		defer timer.Stop()
		select {
		case now := <-timer.C:
			result <- now
		case <-ctx.Done():
		}
		close(result)
	}()
	return result
}
