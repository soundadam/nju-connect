package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

type AccessProbeFunc func(context.Context) error

type AccessEvidenceFunc func(bool)

type AccessProbeConfig struct {
	Probe    AccessProbeFunc
	Evidence AccessEvidenceFunc
	Interval time.Duration
	After    func(context.Context, time.Duration) <-chan time.Time
}

type AccessProbeCoordinator struct {
	config AccessProbeConfig
}

func NewAccessProbeCoordinator(config AccessProbeConfig) (*AccessProbeCoordinator, error) {
	if config.Probe == nil {
		return nil, errors.New("access probe is required")
	}
	if config.Interval <= 0 {
		config.Interval = 5 * time.Minute
	}
	if config.After == nil {
		config.After = afterContext
	}
	return &AccessProbeCoordinator{config: config}, nil
}

type probeResult struct {
	generation uint64
	err        error
}

func (coordinator *AccessProbeCoordinator) Run(ctx context.Context, states <-chan State) error {
	results := make(chan probeResult, 1)
	desired := false
	active := false
	var generation uint64
	var cancelProbe context.CancelFunc
	var timer <-chan time.Time
	var cancelTimer context.CancelFunc
	stopTimer := func() {
		if cancelTimer != nil {
			cancelTimer()
			cancelTimer = nil
		}
		timer = nil
	}
	startProbe := func() {
		if active || !desired {
			return
		}
		stopTimer()
		generation++
		current := generation
		probeContext, cancel := context.WithCancel(ctx)
		cancelProbe = cancel
		active = true
		go func() {
			err := coordinator.config.Probe(probeContext)
			results <- probeResult{generation: current, err: err}
		}()
	}
	defer func() {
		stopTimer()
		if cancelProbe != nil {
			cancelProbe()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			if cancelProbe != nil {
				cancelProbe()
			}
			if active {
				<-results
				active = false
			}
			return ctx.Err()
		case state, ok := <-states:
			if !ok {
				return nil
			}
			desired = state == StateConnected
			if !desired {
				stopTimer()
				if cancelProbe != nil {
					cancelProbe()
				}
			} else {
				startProbe()
			}
		case result := <-results:
			active = false
			cancelProbe = nil
			if result.generation != generation {
				startProbe()
				continue
			}
			if desired && !errors.Is(result.err, context.Canceled) {
				if coordinator.config.Evidence != nil {
					coordinator.config.Evidence(result.err == nil)
				}
				timer, cancelTimer = startWatchdog(ctx, coordinator.config.Interval, coordinator.config.After)
			} else if desired {
				startProbe()
			}
		case <-timer:
			stopTimer()
			startProbe()
		}
	}
}

func NewHEADProbe(client *http.Client, disclosedURL string) (AccessProbeFunc, error) {
	if client == nil {
		return nil, errors.New("access probe HTTP client is required")
	}
	parsed, err := url.Parse(disclosedURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("access probe URL must be a disclosed HTTP(S) URL")
	}
	return func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, disclosedURL, nil)
		if err != nil {
			return errors.New("build access probe")
		}
		response, err := client.Do(request)
		if err != nil {
			return errors.New("access probe failed")
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return errors.New("access probe was not accepted")
		}
		return nil
	}, nil
}
