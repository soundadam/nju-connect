package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

type StreamKind uint8

const (
	StreamRX StreamKind = 0x06
	StreamTX StreamKind = 0x05
)

type StreamWorker interface {
	Run(context.Context) error
	Close() error
}

type StreamFactory interface {
	Open(context.Context, StreamKind) (StreamWorker, error)
}

type CohortConfig struct {
	Factory        StreamFactory
	InitialBackoff time.Duration
	MaximumBackoff time.Duration
	StableFor      time.Duration
	Wait           WaitFunc
	Now            func() time.Time
}

type CohortSupervisor struct {
	config CohortConfig
}

func NewCohortSupervisor(config CohortConfig) (*CohortSupervisor, error) {
	if config.Factory == nil {
		return nil, errors.New("stream factory is required")
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = 4 * time.Second
	}
	if config.MaximumBackoff <= 0 {
		config.MaximumBackoff = 30 * time.Second
	}
	if config.StableFor <= 0 {
		config.StableFor = time.Minute
	}
	if config.Wait == nil {
		config.Wait = waitContext
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &CohortSupervisor{config: config}, nil
}

func (supervisor *CohortSupervisor) Run(ctx context.Context, report func(Component, bool)) error {
	backoff := newBoundedBackoff(supervisor.config.InitialBackoff, supervisor.config.MaximumBackoff)
	for {
		startedAt := supervisor.config.Now()
		rx, tx, err := supervisor.open(ctx)
		if err == nil {
			report(ComponentRX, true)
			report(ComponentTX, true)
			err = runCohort(ctx, rx, tx, func() {
				report(ComponentRX, false)
				report(ComponentTX, false)
			})
		}
		if errors.Is(err, ErrGatewayRejected) {
			return &RenewalRequired{Reason: RenewalGatewayRejected}
		}
		if errors.Is(err, ErrRenewalRequired) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if supervisor.config.Now().Sub(startedAt) >= supervisor.config.StableFor {
			backoff.Reset()
		}
		if err := supervisor.config.Wait(ctx, backoff.Next()); err != nil {
			return err
		}
	}
}

func (supervisor *CohortSupervisor) open(ctx context.Context) (StreamWorker, StreamWorker, error) {
	rx, err := supervisor.config.Factory.Open(ctx, StreamRX)
	if err != nil {
		return nil, nil, err
	}
	tx, err := supervisor.config.Factory.Open(ctx, StreamTX)
	if err != nil {
		_ = rx.Close()
		return nil, nil, err
	}
	return rx, tx, nil
}

func runCohort(ctx context.Context, rx, tx StreamWorker, onBreak func()) error {
	generation, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	start := func(worker StreamWorker) {
		defer workers.Done()
		results <- worker.Run(generation)
	}
	go start(rx)
	go start(tx)

	var first error
	received := 0
	select {
	case first = <-results:
		received = 1
	case <-ctx.Done():
		first = ctx.Err()
	}
	if onBreak != nil {
		onBreak()
	}
	cancel()
	_ = rx.Close()
	_ = tx.Close()
	rejected := errors.Is(first, ErrGatewayRejected)
	for received < 2 {
		result := <-results
		rejected = rejected || errors.Is(result, ErrGatewayRejected)
		received++
	}
	workers.Wait()
	if rejected {
		return ErrGatewayRejected
	}
	return first
}
