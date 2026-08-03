package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccessProbeRunsImmediatelyAndPeriodicallyOnlyWhileConnected(t *testing.T) {
	started := make(chan struct{}, 3)
	evidence := make(chan bool, 3)
	timer := make(chan time.Time, 1)
	coordinator, err := NewAccessProbeCoordinator(AccessProbeConfig{
		Probe: func(context.Context) error {
			started <- struct{}{}
			return nil
		},
		Evidence: func(available bool) { evidence <- available },
		After:    func(context.Context, time.Duration) <-chan time.Time { return timer },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	states := make(chan State, 3)
	result := make(chan error, 1)
	go func() { result <- coordinator.Run(ctx, states) }()
	states <- StateConnected
	waitSignal(t, started, "immediate probe")
	if available := <-evidence; !available {
		t.Fatal("successful probe was reported unavailable")
	}
	timer <- time.Now()
	waitSignal(t, started, "periodic probe")
	if available := <-evidence; !available {
		t.Fatal("periodic probe was reported unavailable")
	}
	states <- StateReconnecting
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinator error = %v", err)
	}
}

func TestAccessProbeIsCanceledDuringReconnectWithoutEvidence(t *testing.T) {
	started := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	evidence := make(chan bool, 1)
	coordinator, err := NewAccessProbeCoordinator(AccessProbeConfig{
		Probe: func(ctx context.Context) error {
			started <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			return ctx.Err()
		},
		Evidence: func(available bool) { evidence <- available },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	states := make(chan State, 4)
	result := make(chan error, 1)
	go func() { result <- coordinator.Run(ctx, states) }()
	states <- StateConnected
	waitSignal(t, started, "first probe")
	states <- StateReconnecting
	waitSignal(t, canceled, "probe cancellation")
	select {
	case value := <-evidence:
		t.Fatalf("canceled probe emitted evidence %v", value)
	case <-time.After(20 * time.Millisecond):
	}
	states <- StateConnected
	waitSignal(t, started, "probe after recovery")
	cancel()
	waitSignal(t, canceled, "final probe cancellation")
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinator error = %v", err)
	}
}

func TestHEADProbeUsesTheConfiguredSOCKSListenerWithoutRequestBody(t *testing.T) {
	requests := make(chan *http.Request, 1)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	dialer := testTCPDialFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	})
	server, err := NewSOCKSServer(SOCKSConfig{Bind: "127.0.0.1:0", Dialer: dialer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- server.Run(ctx, func(Component, bool) {}) }()
	client, err := NewSOCKSHTTPClient(server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewHEADProbe(client, target.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if request.Method != http.MethodHead || request.Body != http.NoBody {
		t.Fatalf("probe request method=%s body=%T", request.Method, request.Body)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("server error = %v", err)
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
