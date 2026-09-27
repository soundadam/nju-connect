package speedtest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

const pinnedServerJSON = `[{"id":1,"name":"NJU Campus IPv4","server":"http://speed.nju.edu.cn","dlURL":"/backend/garbage.php","ulURL":"/backend/empty.php","pingURL":"/backend/empty.php","getIpURL":"/backend/getIP.php"}]`

var helperVersionPattern = regexp.MustCompile(`(?m)^librespeed-cli\s+(v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)\s`)

var (
	ErrDirectUnavailable  = errors.New("NJU campus speed-test service is not directly reachable")
	ErrNJUConnectRequired = errors.New("nju-connect must be connected to reach the NJU campus speed-test service")
	ErrNJUConnectNotReady = errors.New("nju-connect is not ready for campus speed testing")
)

type RuntimeState struct {
	Connected   bool
	SOCKSListen string
}

type RuntimeStatusFunc func() (RuntimeState, error)

type ProbeFunc func(context.Context, Route, string) error

type Service struct {
	HelperPath     string
	Store          Store
	RuntimeStatus  RuntimeStatusFunc
	Probe          ProbeFunc
	Now            func() time.Time
	Timeout        time.Duration
	VersionTimeout time.Duration
}

func (service Service) Run(ctx context.Context, requested Route, sink ProgressSink) (Result, error) {
	if service.Now == nil {
		service.Now = time.Now
	}
	if service.Probe == nil {
		service.Probe = ProbeReachability
	}
	if service.Timeout <= 0 {
		service.Timeout = 60 * time.Second
	}
	if service.VersionTimeout <= 0 {
		// The first execution of a freshly installed helper can be delayed
		// by several seconds of on-demand malware scanning on macOS.
		service.VersionTimeout = 15 * time.Second
	}
	selected, socks, err := service.selectRoute(ctx, requested, sink)
	if err != nil {
		return Result{}, err
	}
	startedAt := service.Now().UTC()
	result, err := runHelper(ctx, helperRunOptions{
		Path: service.HelperPath, Route: selected, SOCKSListen: socks,
		Timeout: service.Timeout, VersionTimeout: service.VersionTimeout, StartedAt: startedAt,
		Now: service.Now, Progress: sink,
	})
	if err != nil {
		return Result{}, err
	}
	if saveErr := service.Store.Save(result); saveErr != nil {
		return Result{}, fmt.Errorf("save latest campus speed-test result: %w", saveErr)
	}
	report(sink, Event{Type: "result", Route: selected, Result: &result})
	return result, nil
}

func (service Service) ProbeRoute(ctx context.Context, requested Route) (ProbeResult, error) {
	probe := service.Probe
	if probe == nil {
		probe = ProbeReachability
	}
	var latency time.Duration
	service.Probe = func(ctx context.Context, route Route, socksListen string) error {
		startedAt := time.Now()
		err := probe(ctx, route, socksListen)
		if err == nil {
			latency = time.Since(startedAt)
		}
		return err
	}
	route, _, err := service.selectRoute(ctx, requested, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{
		SchemaVersion: SchemaVersion,
		Target:        TargetHost,
		Route:         route,
		LatencyMS:     float64(latency) / float64(time.Millisecond),
	}, nil
}

func (service Service) selectRoute(ctx context.Context, requested Route, sink ProgressSink) (Route, string, error) {
	if requested == "" {
		requested = RouteAuto
	}
	if _, err := ParseRoute(string(requested)); err != nil {
		return "", "", err
	}
	if requested == RouteDirect || requested == RouteAuto {
		report(sink, Event{Type: "measurement_progress", Phase: "probing", Route: RouteDirect})
		if err := service.Probe(ctx, RouteDirect, ""); err == nil {
			report(sink, Event{Type: "measurement_progress", Phase: "route_selected", Route: RouteDirect})
			return RouteDirect, "", nil
		} else if ctx.Err() != nil {
			return "", "", ctx.Err()
		} else if requested == RouteDirect {
			return "", "", fmt.Errorf("%w: %v", ErrDirectUnavailable, err)
		}
	}
	if service.RuntimeStatus == nil {
		return "", "", ErrNJUConnectRequired
	}
	state, err := service.RuntimeStatus()
	if err != nil || !state.Connected {
		return "", "", ErrNJUConnectRequired
	}
	if err := validateSOCKSAddress(state.SOCKSListen); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrNJUConnectNotReady, err)
	}
	report(sink, Event{Type: "measurement_progress", Phase: "probing", Route: RouteNJUConnect})
	if err := service.Probe(ctx, RouteNJUConnect, state.SOCKSListen); err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", fmt.Errorf("%w: %v", ErrNJUConnectNotReady, err)
	}
	report(sink, Event{Type: "measurement_progress", Phase: "route_selected", Route: RouteNJUConnect})
	return RouteNJUConnect, state.SOCKSListen, nil
}

func ProbeReachability(ctx context.Context, route Route, socksListen string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	switch route {
	case RouteDirect:
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		}
	case RouteNJUConnect:
		if err := validateSOCKSAddress(socksListen); err != nil {
			return err
		}
		dialer, err := proxy.SOCKS5("tcp", socksListen, nil, &net.Dialer{Timeout: 2 * time.Second})
		if err != nil {
			return err
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return errors.New("SOCKS dialer does not support cancellation")
		}
		transport.DialContext = contextDialer.DialContext
	default:
		return errors.New("reachability probe route is invalid")
	}
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, TargetURL+"/backend/empty.php", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	return nil
}

func validateSOCKSAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return errors.New("runtime SOCKS address is invalid")
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil || !parsed.IsLoopback() {
		return errors.New("runtime SOCKS address is not numeric loopback")
	}
	return nil
}

type helperRunOptions struct {
	Path, SOCKSListen       string
	Route                   Route
	Timeout, VersionTimeout time.Duration
	StartedAt               time.Time
	Now                     func() time.Time
	Progress                ProgressSink
}

func runHelper(ctx context.Context, options helperRunOptions) (Result, error) {
	if options.Path == "" {
		return Result{}, ErrComponentMissing
	}
	if ctx.Err() != nil {
		return cancelledResult(options), nil
	}
	versionCtx, cancelVersion := context.WithTimeout(ctx, options.VersionTimeout)
	versionCommand := exec.CommandContext(versionCtx, options.Path, "--version")
	versionOutput, versionErr := versionCommand.CombinedOutput()
	cancelVersion()
	if versionErr != nil {
		if ctx.Err() != nil || errors.Is(versionCtx.Err(), context.Canceled) {
			return cancelledResult(options), nil
		}
		if errors.Is(versionCtx.Err(), context.DeadlineExceeded) {
			return Result{}, errors.New("campus speed-test component did not respond to a version check; try again")
		}
		return Result{}, fmt.Errorf("inspect campus speed-test component: %w", versionErr)
	}
	match := helperVersionPattern.FindSubmatch(versionOutput)
	if len(match) != 2 || string(match[1]) != HelperVersion {
		return Result{}, fmt.Errorf("campus speed-test component version is unsupported")
	}
	args := []string{
		"--local-json", "-", "--server", "1", "--duration", "10", "--concurrent", "3",
		"--no-icmp", "--telemetry-level", "disabled", "--json", "--progress-json", "--ipv4",
	}
	if options.Route == RouteNJUConnect {
		args = append(args, "--proxy", "socks5h://"+options.SOCKSListen)
	}
	measurementCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	command := exec.CommandContext(measurementCtx, options.Path, args...)
	command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	command.Stdin = strings.NewReader(pinnedServerJSON)
	var stdout bytes.Buffer
	stderr, err := command.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	command.Stdout = &stdout
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("start campus speed-test component: %w", err)
	}
	report(options.Progress, Event{Type: "measurement_progress", Phase: "measuring", Route: options.Route})
	var helperErrors bytes.Buffer
	progressErr := scanProgress(stderr, &helperErrors, options.Route, options.Progress)
	waitErr := command.Wait()
	endedAt := options.Now().UTC()
	if progressErr != nil {
		return Result{}, progressErr
	}
	if waitErr != nil {
		status := StatusFailed
		failure := classifyHelperFailure(helperErrors.String())
		if errors.Is(measurementCtx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = StatusCancelled
			failure = &Failure{Stage: "cancelled", Code: "cancelled", Message: "campus speed test was cancelled"}
		} else if errors.Is(measurementCtx.Err(), context.DeadlineExceeded) {
			failure = &Failure{Stage: "timeout", Code: "timeout", Message: "campus speed test timed out"}
		}
		result := baseResult(options, endedAt)
		result.Status, result.Failure = status, failure
		return result, nil
	}
	result, err := parseHelperResult(stdout.Bytes(), options, endedAt)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func cancelledResult(options helperRunOptions) Result {
	result := baseResult(options, options.Now().UTC())
	result.Status = StatusCancelled
	result.Failure = &Failure{Stage: "cancelled", Code: "cancelled", Message: "campus speed test was cancelled"}
	return result
}

func baseResult(options helperRunOptions, endedAt time.Time) Result {
	return Result{
		SchemaVersion: SchemaVersion, StartedAt: options.StartedAt, EndedAt: endedAt,
		Target: TargetHost, Family: "ipv4", Route: options.Route, HelperVersion: HelperVersion,
	}
}

type progressReport struct {
	Type      string  `json:"type"`
	Test      string  `json:"test"`
	ElapsedMS int64   `json:"elapsed_ms"`
	Bytes     uint64  `json:"bytes"`
	Mbps      float64 `json:"mbps"`
}

func scanProgress(input io.Reader, ordinary *bytes.Buffer, route Route, sink ProgressSink) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 16<<10), 256<<10)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &envelope) != nil || envelope.Type != "progress" {
			if ordinary != nil && ordinary.Len() < 16<<10 {
				remaining := (16 << 10) - ordinary.Len()
				if len(line) > remaining {
					line = line[:remaining]
				}
				ordinary.Write(line)
				ordinary.WriteByte('\n')
			}
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var value progressReport
		if err := decoder.Decode(&value); err != nil || value.ElapsedMS <= 0 || (value.Test != "download" && value.Test != "upload") || math.IsNaN(value.Mbps) || math.IsInf(value.Mbps, 0) || value.Mbps < 0 {
			return errors.New("campus speed-test component emitted invalid progress")
		}
		var rate *float64
		if value.Bytes > 0 {
			rate = &value.Mbps
		}
		report(sink, Event{Type: "measurement_progress", Phase: value.Test, Test: value.Test, Mbps: rate, Route: route})
	}
	return scanner.Err()
}

func classifyHelperFailure(message string) *Failure {
	value := strings.ToLower(message)
	switch {
	case strings.Contains(value, "download"):
		return &Failure{Stage: "download", Code: "download_failure", Message: "campus download measurement failed"}
	case strings.Contains(value, "upload"):
		return &Failure{Stage: "upload", Code: "upload_failure", Message: "campus upload measurement failed"}
	case strings.Contains(value, "connect"), strings.Contains(value, "dial"), strings.Contains(value, "resolve"):
		return &Failure{Stage: "connect", Code: "connect_failure", Message: "campus speed-test service could not be reached"}
	default:
		return &Failure{Stage: "helper", Code: "helper_failed", Message: "campus speed-test component exited before producing a result"}
	}
}

type helperReport struct {
	Server struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"server"`
	Ping     float64 `json:"ping"`
	Jitter   float64 `json:"jitter"`
	Upload   float64 `json:"upload"`
	Download float64 `json:"download"`
}

func parseHelperResult(data []byte, options helperRunOptions, endedAt time.Time) (Result, error) {
	var reports []helperReport
	if err := json.Unmarshal(data, &reports); err != nil || len(reports) != 1 {
		return Result{}, errors.New("campus speed-test component returned an invalid result")
	}
	reportValue := reports[0]
	serverURL, err := url.Parse(reportValue.Server.URL)
	if err != nil || !strings.EqualFold(serverURL.Hostname(), TargetHost) || reportValue.Server.Name == "" {
		return Result{}, errors.New("campus speed-test component returned an unexpected server")
	}
	for _, metric := range []float64{reportValue.Ping, reportValue.Jitter, reportValue.Upload, reportValue.Download} {
		if metric < 0 || math.IsNaN(metric) || math.IsInf(metric, 0) {
			return Result{}, errors.New("campus speed-test component returned an invalid metric")
		}
	}
	result := baseResult(options, endedAt)
	result.Status = StatusSuccess
	result.Server = TargetHost
	result.PingMS = &reportValue.Ping
	result.JitterMS = &reportValue.Jitter
	result.UploadMbps = &reportValue.Upload
	result.DownloadMbps = &reportValue.Download
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}
