package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type controlledWorker struct {
	done      chan error
	closed    chan struct{}
	joined    chan struct{}
	closeOnce sync.Once
}

func newControlledWorker() *controlledWorker {
	return &controlledWorker{done: make(chan error, 1), closed: make(chan struct{}), joined: make(chan struct{})}
}

func (worker *controlledWorker) Run(context.Context) error {
	err := <-worker.done
	close(worker.joined)
	return err
}

func (worker *controlledWorker) Close() error {
	worker.closeOnce.Do(func() { close(worker.closed) })
	return nil
}

type cohortFactory struct {
	mu         sync.Mutex
	workers    []*controlledWorker
	openCount  int
	openedNext chan struct{}
}

type streamFactoryFunc func(context.Context, StreamKind) (StreamWorker, error)

func (open streamFactoryFunc) Open(ctx context.Context, kind StreamKind) (StreamWorker, error) {
	return open(ctx, kind)
}

func (factory *cohortFactory) Open(context.Context, StreamKind) (StreamWorker, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	worker := factory.workers[factory.openCount]
	factory.openCount++
	if factory.openCount == 3 {
		close(factory.openedNext)
	}
	return worker, nil
}

type immediateWorker struct {
	err error
}

func (worker immediateWorker) Run(context.Context) error { return worker.err }
func (immediateWorker) Close() error                     { return nil }

func TestCohortJoinsPeerBeforeOpeningNextGeneration(t *testing.T) {
	workers := []*controlledWorker{newControlledWorker(), newControlledWorker(), newControlledWorker(), newControlledWorker()}
	factory := &cohortFactory{workers: workers, openedNext: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor, err := NewCohortSupervisor(CohortConfig{
		Factory: factory,
		Wait:    func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	notReady := make(chan Component, 2)
	result := make(chan error, 1)
	go func() {
		result <- supervisor.Run(ctx, func(component Component, ready bool) {
			if !ready {
				notReady <- component
			}
		})
	}()
	workers[0].done <- errors.New("rx transport ended")
	select {
	case <-workers[1].closed:
	case <-time.After(time.Second):
		t.Fatal("peer was not closed")
	}
	for range 2 {
		select {
		case <-notReady:
		case <-time.After(time.Second):
			t.Fatal("cohort readiness was not cleared before peer join")
		}
	}
	select {
	case <-factory.openedNext:
		t.Fatal("next generation opened before peer joined")
	case <-time.After(20 * time.Millisecond):
	}
	workers[1].done <- errors.New("tx stopped")
	select {
	case <-factory.openedNext:
	case <-time.After(time.Second):
		t.Fatal("next generation did not open after peer joined")
	}
	cancel()
	workers[2].done <- context.Canceled
	workers[3].done <- context.Canceled
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

func TestCohortPropagatesRenewalWithoutRetry(t *testing.T) {
	waited := false
	supervisor, err := NewCohortSupervisor(CohortConfig{
		Factory: streamFactoryFunc(func(context.Context, StreamKind) (StreamWorker, error) {
			return nil, &RenewalRequired{Reason: RenewalGatewayRejected}
		}),
		Wait: func(context.Context, time.Duration) error {
			waited = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = supervisor.Run(context.Background(), func(Component, bool) {})
	var renewal *RenewalRequired
	if !errors.As(err, &renewal) || renewal.Reason != RenewalGatewayRejected {
		t.Fatalf("cohort error = %v", err)
	}
	if waited {
		t.Fatal("cohort retried an explicit renewal")
	}
}

func TestCohortStableWindowStartsAfterGenerationOpens(t *testing.T) {
	stop := errors.New("stop test")
	now := time.Unix(1000, 0)
	openCount := 0
	var delays []time.Duration
	supervisor, err := NewCohortSupervisor(CohortConfig{
		Factory: streamFactoryFunc(func(context.Context, StreamKind) (StreamWorker, error) {
			openCount++
			if openCount == 1 {
				return nil, errors.New("temporary transport failure")
			}
			if openCount == 2 {
				now = now.Add(2 * time.Minute)
			}
			return immediateWorker{err: errors.New("stream ended")}, nil
		}),
		StableFor: time.Minute,
		Now:       func() time.Time { return now },
		Wait: func(_ context.Context, duration time.Duration) error {
			delays = append(delays, duration)
			if len(delays) == 2 {
				return stop
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(context.Background(), func(Component, bool) {}); !errors.Is(err, stop) {
		t.Fatalf("run error = %v", err)
	}
	want := []time.Duration{4 * time.Second, 8 * time.Second}
	if len(delays) != len(want) || delays[0] != want[0] || delays[1] != want[1] {
		t.Fatalf("backoff delays = %v, want %v", delays, want)
	}
}
